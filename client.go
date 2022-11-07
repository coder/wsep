package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"

	"cdr.dev/wsep/internal/proto"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"
)

const maxMessageSize = 64000

type remoteExec struct {
	conn *websocket.Conn
}

// RemoteExecer creates an execution interface from a WebSocket connection.
func RemoteExecer(conn *websocket.Conn) Execer {
	conn.SetReadLimit(maxMessageSize)
	return remoteExec{conn: conn}
}

// Command represents an external command to be run
type Command struct {
	// ID allows reconnecting commands that have a TTY.
	ID         string
	Command    string
	Args       []string
	TTY        bool
	Stdin      bool
	UID        uint32
	GID        uint32
	Env        []string
	WorkingDir string
}

// Start runs the command on the remote.  Once a command is started, callers should
// not read from, write to, or close the websocket.  Closing the returned Process will
// also close the websocket.
func (r remoteExec) Start(ctx context.Context, c Command) (Process, error) {
	header := proto.ClientStartHeader{
		ID:      c.ID,
		Command: mapToProtoCmd(c),
		Type:    proto.TypeStart,
	}
	payload, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	err = r.conn.Write(ctx, websocket.MessageBinary, payload)
	if err != nil {
		return nil, err
	}

	_, payload, err = r.conn.Read(ctx)
	if err != nil {
		return nil, xerrors.Errorf("read pid message: %w", err)
	}
	var pidHeader proto.ServerPidHeader
	err = json.Unmarshal(payload, &pidHeader)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse pid message: %w", err)
	}

	var stdin io.WriteCloser
	if c.Stdin {
		stdin = remoteStdin{
			conn: websocket.NetConn(ctx, r.conn, websocket.MessageBinary),
		}
	} else {
		stdin = disabledStdinWriter{}
	}

	listenCtx, cancelListen := context.WithCancel(ctx)
	rp := &remoteProcess{
		ctx:          ctx,
		conn:         r.conn,
		cmd:          c,
		pid:          pidHeader.Pid,
		done:         make(chan struct{}),
		stderr:       newPipe(),
		stderrData:   make(chan []byte),
		stdout:       newPipe(),
		stdoutData:   make(chan []byte),
		stdin:        stdin,
		cancelListen: cancelListen,
	}

	go rp.listen(listenCtx)
	return rp, nil
}

type remoteProcess struct {
	ctx          context.Context
	cancelListen func()
	cmd          Command
	conn         *websocket.Conn
	pid          int
	done         chan struct{}
	closeErr     error
	exitCode     *int
	readErr      error
	stdin        io.WriteCloser
	stdout       pipe
	stdoutErr    error
	stdoutData   chan []byte
	stderr       pipe
	stderrErr    error
	stderrData   chan []byte
}

type remoteStdin struct {
	conn net.Conn
}

func (r remoteStdin) Write(b []byte) (int, error) {
	stdinHeader := proto.Header{
		Type: proto.TypeStdin,
	}

	headerByt, err := json.Marshal(stdinHeader)
	if err != nil {
		return 0, err
	}
	stdinWriter := proto.WithHeader(r.conn, headerByt)

	maxBodySize := maxMessageSize - len(headerByt) - 1
	var nn int
	for len(b) > maxMessageSize {
		n, err := stdinWriter.Write(b[:maxBodySize])
		nn += n
		if err != nil {
			return nn, err
		}
		b = b[maxBodySize:]
	}

	n, err := stdinWriter.Write(b)
	nn += n
	return nn, err
}

func (r remoteStdin) Close() error {
	closeHeader := proto.Header{
		Type: proto.TypeCloseStdin,
	}
	headerByt, err := json.Marshal(closeHeader)
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(r.conn, headerByt).Write(nil)
	return err
}

type pipe struct {
	r   *io.PipeReader
	w   *io.PipeWriter
	d   chan []byte
	e   chan error
	buf []byte
}

func newPipe() pipe {
	pr, pw := io.Pipe()
	return pipe{
		r:   pr,
		w:   pw,
		d:   make(chan []byte),
		e:   make(chan error),
		buf: make([]byte, maxMessageSize),
	}
}

// writeCtx writes data to the pipe, or returns if the context is canceled.
func (p *pipe) writeCtx(ctx context.Context, data []byte) error {
	// actually do the copy on another goroutine so that we can return if context
	// is canceled
	go func() {
		var err error
		select {
		case <-ctx.Done():
			return
		case body := <-p.d:
			_, err = io.CopyBuffer(p.w, bytes.NewReader(body), p.buf)
		}
		select {
		case <-ctx.Done():
			return
		case p.e <- err:
			return
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.d <- data:
		// data being written.
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-p.e:
		return err
	}
}

func (r *remoteProcess) listen(ctx context.Context) {
	defer func() {
		r.stdoutErr = r.stdout.w.Close()
		r.stderrErr = r.stderr.w.Close()

		r.closeErr = r.conn.Close(websocket.StatusNormalClosure, "normal closure")
		// If we were in r.conn.Read() we cancel the ctx, the websocket library closes
		// the websocket before we have a chance to.  This is a normal closure.
		if r.closeErr != nil && strings.Contains(r.closeErr.Error(), "already wrote close") &&
			r.readErr != nil && strings.Contains(r.readErr.Error(), "context canceled") {
			r.closeErr = nil
		}
		close(r.done)
	}()

	for ctx.Err() == nil {
		_, payload, err := r.conn.Read(ctx)
		if err != nil {
			r.readErr = err
			return
		}
		headerByt, body := proto.SplitMessage(payload)

		var header proto.Header
		err = json.Unmarshal(headerByt, &header)
		if err != nil {
			r.readErr = err
			return
		}

		switch header.Type {
		case proto.TypeStderr:
			err = r.stderr.writeCtx(ctx, body)
			if err != nil {
				r.readErr = err
				return
			}
		case proto.TypeStdout:
			err = r.stdout.writeCtx(ctx, body)
			if err != nil {
				r.readErr = err
				return
			}
		case proto.TypeExitCode:
			var exitMsg proto.ServerExitCodeHeader
			err = json.Unmarshal(headerByt, &exitMsg)
			if err != nil {
				r.readErr = err
				return
			}

			r.exitCode = &exitMsg.ExitCode
			return
		}
	}
	// if we get here, the context is done, so use that as the read error
	r.readErr = ctx.Err()
}

func (r *remoteProcess) Pid() int {
	return r.pid
}

func (r *remoteProcess) Stdin() io.WriteCloser {
	if !r.cmd.Stdin {
		return disabledStdinWriter{}
	}
	return r.stdin
}

// Stdout returns a reader for standard out from the process.  You MUST read from
// this reader even if you don't care about the data to avoid blocking the
// websocket.
func (r *remoteProcess) Stdout() io.Reader {
	return r.stdout.r
}

// Stdout returns a reader for standard error from the process.  You MUST read from
// this reader even if you don't care about the data to avoid blocking the
// websocket.
func (r *remoteProcess) Stderr() io.Reader {
	return r.stderr.r
}

func (r *remoteProcess) Resize(ctx context.Context, rows, cols uint16) error {
	header := proto.ClientResizeHeader{
		Type: proto.TypeResize,
		Cols: cols,
		Rows: rows,
	}
	payload, err := json.Marshal(header)
	if err != nil {
		return err
	}
	return r.conn.Write(ctx, websocket.MessageBinary, payload)
}

func (r *remoteProcess) Wait() error {
	<-r.done
	if r.readErr != nil {
		return r.readErr
	}
	// when listen() closes r.done, either there must be a read error
	// or exitCode is set non-nil, so it's safe to dereference the pointer
	// here
	if *r.exitCode != 0 {
		return ExitError{Code: *r.exitCode}
	}
	return nil
}

func (r *remoteProcess) Close() error {
	r.cancelListen()
	<-r.done
	closeErr := r.closeErr
	return joinErrs(closeErr, r.stdoutErr, r.stderrErr)
}

func joinErrs(errs ...error) error {
	var str string
	foundErr := false
	for _, e := range errs {
		if e != nil {
			foundErr = true
			if str != "" {
				str += ", "
			}
			str += e.Error()
		}
	}
	if foundErr {
		return xerrors.New(str)
	}
	return nil
}
