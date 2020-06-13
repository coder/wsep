package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"

	"cdr.dev/wsep/internal/proto"
	"golang.org/x/sync/errgroup"
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
	Command    string
	Args       []string
	TTY        bool
	Stdin      bool
	UID        uint32
	GID        uint32
	Env        []string
	WorkingDir string
}

func (r remoteExec) Start(ctx context.Context, c Command) (Process, error) {
	header := proto.ClientStartHeader{
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

	rp := remoteProcess{
		ctx:    ctx,
		conn:   r.conn,
		cmd:    c,
		pid:    pidHeader.Pid,
		done:   make(chan error),
		stderr: newPipe(),
		stdout: newPipe(),
		stdin:  stdin,
	}

	go rp.listen(ctx)
	return rp, nil
}

type remoteProcess struct {
	ctx    context.Context
	cmd    Command
	conn   *websocket.Conn
	pid    int
	done   chan error
	stdin  io.WriteCloser
	stdout pipe
	stderr pipe
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
	r *io.PipeReader
	w *io.PipeWriter
}

func newPipe() pipe {
	pr, pw := io.Pipe()
	return pipe{
		r: pr,
		w: pw,
	}
}

func (r remoteProcess) listen(ctx context.Context) {
	defer r.conn.Close(websocket.StatusNormalClosure, "normal closure")

	exitCode := make(chan int, 1)
	var eg errgroup.Group

	eg.Go(func() error {
		defer r.stdout.w.Close()
		defer r.stderr.w.Close()

		buf := make([]byte, maxMessageSize) // max size of one websocket message
		for ctx.Err() == nil {
			_, payload, err := r.conn.Read(ctx)
			if err != nil {
				continue
			}
			headerByt, body := proto.SplitMessage(payload)

			var header proto.Header
			err = json.Unmarshal(headerByt, &header)
			if err != nil {
				continue
			}

			switch header.Type {
			case proto.TypeStderr:
				_, err = io.CopyBuffer(r.stderr.w, bytes.NewReader(body), buf)
				if err != nil {
					return err
				}
			case proto.TypeStdout:
				_, err = io.CopyBuffer(r.stdout.w, bytes.NewReader(body), buf)
				if err != nil {
					return err
				}
			case proto.TypeExitCode:
				var exitMsg proto.ServerExitCodeHeader
				err = json.Unmarshal(headerByt, &exitMsg)
				if err != nil {
					continue
				}

				exitCode <- exitMsg.ExitCode
				return nil
			}
		}
		return ctx.Err()
	})

	err := eg.Wait()
	select {
	case exitCode := <-exitCode:
		if exitCode != 0 {
			r.done <- ExitError{Code: int(exitCode)}
			return
		}
		r.done <- nil
	default:
		r.done <- err
	}
}

func (r remoteProcess) Pid() int {
	return r.pid
}

func (r remoteProcess) Stdin() io.WriteCloser {
	if !r.cmd.Stdin {
		return disabledStdinWriter{}
	}
	return r.stdin
}

func (r remoteProcess) Stdout() io.Reader {
	return r.stdout.r
}

func (r remoteProcess) Stderr() io.Reader {
	return r.stderr.r
}

func (r remoteProcess) Resize(ctx context.Context, rows, cols uint16) error {
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

func (r remoteProcess) Wait() error {
	select {
	case err := <-r.done:
		return err
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

func (r remoteProcess) Close() error {
	err := r.conn.Close(websocket.StatusNormalClosure, "")
	err1 := r.stderr.w.Close()
	err2 := r.stdout.w.Close()
	return joinErrs(err, err1, err2)
}

func joinErrs(errs ...error) error {
	var str string
	for _, e := range errs {
		if e != nil {
			if str != "" {
				str += ", "
			}
			str += e.Error()
		}
	}
	return xerrors.New(str)
}
