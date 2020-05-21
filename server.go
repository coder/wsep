package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	"go.coder.com/flog"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"

	"cdr.dev/wsep/internal/proto"
)

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer) error {
	var (
		header    proto.Header
		process   Process
		wsNetConn = websocket.NetConn(ctx, c, websocket.MessageBinary)
	)
	defer func() {
		if process != nil {
			process.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, byt, err := c.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status == -1 {
				return xerrors.Errorf("read message: %w", err)
			}
			if status != websocket.StatusNormalClosure {
				return err
			}
			return nil
		}
		headerByt, bodyByt := proto.SplitMessage(byt)

		err = json.Unmarshal(headerByt, &header)
		if err != nil {
			return xerrors.Errorf("unmarshal header: %w", err)
		}

		switch header.Type {
		case proto.TypeStart:
			var header proto.ClientStartHeader
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal start header: %w", err)
			}
			process, err = execer.Start(ctx, header.Command)
			if err != nil {
				return err
			}

			sendPID(ctx, process.Pid(), wsNetConn)
			go pipeProcessOutput(ctx, process, wsNetConn)

			go func() {
				defer wsNetConn.Close()
				err = process.Wait()
				if exitErr, ok := err.(*ExitError); ok {
					sendExitCode(ctx, exitErr.Code, wsNetConn)
					return
				}
				sendExitCode(ctx, 0, wsNetConn)
			}()
		case proto.TypeResize:
			if process == nil {
				return errors.New("resize sent before command started")
			}

			var header proto.ClientResizeHeader
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal resize header: %w", err)
			}

			err = process.Resize(ctx, header.Rows, header.Cols)
			if err != nil {
				return xerrors.Errorf("resize: %w", err)
			}
		case proto.TypeStdin:
			_, err := io.Copy(process.Stdin(), bytes.NewReader(bodyByt))
			if err != nil {
				return xerrors.Errorf("read stdin: %w", err)
			}
		case proto.TypeCloseStdin:
			wr, err := c.Writer(ctx, websocket.MessageBinary)
			if err != nil {
				return xerrors.Errorf("get writer: %w", err)
			}
			err = json.NewEncoder(wr).Encode(&proto.Header{Type: "close_stdin"})
			if err != nil {
				return xerrors.Errorf("encode close_stdin: %w", err)
			}
		default:
			flog.Error("unrecognized header type: %s", header.Type)
		}
	}
}

func sendExitCode(ctx context.Context, exitCode int, conn net.Conn) {
	header, _ := json.Marshal(proto.ServerExitCodeHeader{
		Type:     proto.TypeExitCode,
		ExitCode: exitCode,
	})
	proto.WithHeader(conn, header).Write(nil)
}

func sendPID(ctx context.Context, pid int, conn net.Conn) {
	header, _ := json.Marshal(proto.ServerPidHeader{Type: proto.TypePid, Pid: pid})
	proto.WithHeader(conn, header).Write(nil)
}

func pipeProcessOutput(ctx context.Context, process Process, conn net.Conn) {
	var (
		stdout = process.Stdout()
		stderr = process.Stderr()
		wg     sync.WaitGroup
	)

	wg.Add(2)
	go pipeReaderWithHeader(stdout, conn, proto.Header{Type: proto.TypeStdout}, &wg)
	go pipeReaderWithHeader(stderr, conn, proto.Header{Type: proto.TypeStderr}, &wg)
	wg.Wait()
}

func pipeReaderWithHeader(r io.Reader, w io.WriteCloser, header proto.Header, wg *sync.WaitGroup) {
	defer wg.Done()
	headerByt, err := json.Marshal(header)
	if err != nil {
		return
	}
	wr := proto.WithHeader(w, headerByt)
	io.Copy(wr, r)
}
