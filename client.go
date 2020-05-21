package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

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
		stdout: bytes.NewBuffer(nil),
		stderr: bytes.NewBuffer(nil),
	}

	go rp.listen(ctx)
	return rp, nil
}

type remoteProcess struct {
	conn   *websocket.Conn
	pid    int
	done   chan error
	stdin  io.WriteCloser
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (r remoteProcess) listen(ctx context.Context) {
	defer r.conn.Close(websocket.StatusNormalClosure, "normal closure")
	for {
		if ctx.Err() != nil {
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
			_, err = r.stderr.Write(body)
			if err != nil {
				flog.Error("failed to write to stderr buffer: %v", err)
				continue
			}
		case proto.TypeStdout:
			_, err = r.stdout.Write(body)
			if err != nil {
				flog.Error("failed to write to stdout buffer: %v", err)
				continue
			}
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
	return nil
}

func (r remoteProcess) Stdout() io.Reader {
	return r.stdout
}

func (r remoteProcess) Stderr() io.Reader {
	return r.stderr
}

func (r remoteProcess) Resize(rows, cols uint16) error {
	return nil
}

func (r remoteProcess) Wait() error {
	return <-r.done
}

func (r remoteProcess) Close() error {
	return r.conn.Close(websocket.StatusAbnormalClosure, "kill process")
}
