package wsep

import (
	"context"
	"encoding/json"
	"io"
	"net"

	"cdr.dev/wsep/internal/proto"
	"go.coder.com/flog"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"
)

type remoteExec struct {
	conn *websocket.Conn
}

// RemoteExecer creates an execution interface from a WebSocket connection.
func RemoteExecer(conn *websocket.Conn) Execer {
	return remoteExec{conn: conn}
}

func (r remoteExec) Start(ctx context.Context, c proto.Command) (Process, error) {
	header := proto.ClientStartHeader{
		Command: c,
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
	rp := remoteProcess{
		conn:   r.conn,
		pid:    pidHeader.Pid,
		done:   make(chan error),
		stderr: newPipe(),
		stdout: newPipe(),
		stdin: remoteStdin{
			conn: websocket.NetConn(ctx, r.conn, websocket.MessageBinary),
		},
	}

	go rp.listen(ctx)
	return rp, nil
}

type remoteProcess struct {
	conn   *websocket.Conn
	pid    int
	done   chan error
	stdin  remoteStdin
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
	return stdinWriter.Write(b)
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
	defer r.stdout.w.Close()
	defer r.stderr.w.Close()

	for {
		if err := ctx.Err(); err != nil {
			r.done <- xerrors.Errorf("process canceled: %w", err)
			break
		}
		_, payload, err := r.conn.Read(ctx)
		if err != nil {
			continue
		}
		headerByt, body := proto.SplitMessage(payload)

		var header proto.Header
		err = json.Unmarshal(headerByt, &header)
		if err != nil {
			flog.Fatal("failed to unmarshal header: %w", err)
		}

		switch header.Type {
		case proto.TypeStderr:
			go r.stderr.w.Write(body)
		case proto.TypeStdout:
			go r.stdout.w.Write(body)
		case proto.TypeExitCode:
			var exitMsg proto.ServerExitCodeHeader
			err = json.Unmarshal(headerByt, &exitMsg)
			if err != nil {
				flog.Error("failed to unmarshal exit code message: %v", err)
				continue
			}

			var err error = ExitError{Code: exitMsg.ExitCode}
			if exitMsg.ExitCode == 0 {
				err = nil
			}
			r.done <- err
			return
		}
	}
}

func (r remoteProcess) Pid() int {
	return r.pid
}

func (r remoteProcess) Stdin() io.WriteCloser {
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
	return <-r.done
}

func (r remoteProcess) Close() error {
	return r.conn.Close(websocket.StatusAbnormalClosure, "kill process")
}
