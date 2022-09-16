package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"go.coder.com/flog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"

	"cdr.dev/wsep/internal/proto"
)

// Options allows configuring the server.
type Options struct {
	SessionTimeout time.Duration
}

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer, options *Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if options == nil {
		options = &Options{}
	}
	if options.SessionTimeout == 0 {
		options.SessionTimeout = 5 * time.Minute
	}

	c.SetReadLimit(maxMessageSize)
	var (
		header    proto.Header
		process   Process
		wsNetConn = websocket.NetConn(ctx, c, websocket.MessageBinary)
	)
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
			if process != nil {
				return errors.New("command already started")
			}

			var header proto.ClientStartHeader
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal start header: %w", err)
			}

			command := mapToClientCmd(header.Command)

			if command.TTY {
				// Enforce rows and columns so the TTY will be correctly sized.
				if command.Rows == 0 || command.Cols == 0 {
					return xerrors.Errorf("rows and cols must be non-zero: %w", err)
				}
			}

			process, err = execer.Start(ctx, command)
			if err != nil {
				return err
			}
			defer process.Close()

			err = sendPID(ctx, process.Pid(), wsNetConn)
			if err != nil {
				flog.Error("failed to send pid %d", process.Pid())
			}

			var outputgroup errgroup.Group
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stdout(), wsNetConn, proto.Header{Type: proto.TypeStdout})
			})
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stderr(), wsNetConn, proto.Header{Type: proto.TypeStderr})
			})

			go func() {
				defer wsNetConn.Close()
				_ = outputgroup.Wait()
				err := process.Wait()
				_ = sendExitCode(ctx, err, wsNetConn)
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

func sendExitCode(_ context.Context, err error, conn net.Conn) error {
	exitCode := 0
	errorStr := ""
	if err != nil {
		errorStr = err.Error()
	}
	if exitErr, ok := err.(ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	header, err := json.Marshal(proto.ServerExitCodeHeader{
		Type:     proto.TypeExitCode,
		ExitCode: exitCode,
		Error:    errorStr,
	})
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(conn, header).Write(nil)
	return err
}

func sendPID(_ context.Context, pid int, conn net.Conn) error {
	header, err := json.Marshal(proto.ServerPidHeader{Type: proto.TypePid, Pid: pid})
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(conn, header).Write(nil)
	return err
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
