package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"go.coder.com/flog"
	"golang.org/x/sync/errgroup"
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
		if xerrors.Is(err, io.EOF) {
			return nil
		}
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
			process, err = execer.Start(ctx, mapToClientCmd(header.Command))
			if err != nil {
				return err
			}

			sendPID(ctx, process.Pid(), wsNetConn)

			var outputgroup errgroup.Group
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stdout(), wsNetConn, proto.Header{Type: proto.TypeStdout})
			})
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stderr(), wsNetConn, proto.Header{Type: proto.TypeStdout})
			})

			go func() {
				defer wsNetConn.Close()
				_ = outputgroup.Wait()
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
			err = process.Stdin().Close()
			if err != nil {
				return xerrors.Errorf("close stdin: %w", err)
			}
		default:
			flog.Error("unrecognized header type: %s", header.Type)
		}
	}
}

func sendExitCode(ctx context.Context, exitCode int, conn net.Conn) {
	header, err := json.Marshal(proto.ServerExitCodeHeader{
		Type:     proto.TypeExitCode,
		ExitCode: exitCode,
	})
	if err != nil {
		return
	}
	proto.WithHeader(conn, header).Write(nil)
}

func sendPID(ctx context.Context, pid int, conn net.Conn) {
	header, err := json.Marshal(proto.ServerPidHeader{Type: proto.TypePid, Pid: pid})
	if err != nil {
		return
	}
	proto.WithHeader(conn, header).Write(nil)
}

func copyWithHeader(r io.Reader, w io.Writer, header proto.Header) error {
	headerByt, err := json.Marshal(header)
	if err != nil {
		return err
	}
	wr := proto.WithHeader(w, headerByt)
	_, err = io.Copy(wr, r)
	if err != nil {
		return err
	}
	return nil
}
