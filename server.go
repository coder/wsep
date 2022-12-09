package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"sync"
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

// _sessions is a global map of sessions that exists for backwards
// compatibility.  Server should be used instead which locally maintains the
// map.
var _sessions sync.Map

// _sessionsMutex is a global mutex that exists for backwards compatibility.
// Server should be used instead which locally maintains the mutex.
var _sessionsMutex sync.Mutex

// Serve runs the server-side of wsep.
// Deprecated: Use Server.Serve() instead.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer, options *Options) error {
	srv := Server{sessions: &_sessions, sessionsMutex: &_sessionsMutex}
	return srv.Serve(ctx, c, execer, options)
}

// Server runs the server-side of wsep.  The execer may be another wsep
// connection for chaining.  Use LocalExecer for local command execution.
type Server struct {
	sessions      *sync.Map
	sessionsMutex *sync.Mutex
}

// NewServer returns as new wsep server.
func NewServer() *Server {
	return &Server{
		sessions:      &sync.Map{},
		sessionsMutex: &sync.Mutex{},
	}
}

// SessionCount returns the number of sessions.
func (srv *Server) SessionCount() int {
	var i int
	srv.sessions.Range(func(k, rawSession interface{}) bool {
		i++
		return true
	})
	return i
}

// Close closes all sessions.
func (srv *Server) Close() {
	srv.sessions.Range(func(k, rawSession interface{}) bool {
		if s, ok := rawSession.(*Session); ok {
			s.Close("test cleanup")
		}
		return true
	})
}

// Serve runs the server-side of wsep.  The execer may be another wsep
// connection for chaining.  Use LocalExecer for local command execution.  The
// web socket will not be closed automatically; the caller must call Close() on
// the web socket (ideally with a reason) once Serve yields.
func (srv *Server) Serve(ctx context.Context, c *websocket.Conn, execer Execer, options *Options) error {
	// The process will get killed when the connection context ends.
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
					return xerrors.Errorf("rows and cols must be non-zero")
				}
			}

			// Only TTYs with IDs can be reconnected.
			if command.TTY && header.ID != "" {
				process, err = srv.withSession(ctx, header.ID, command, execer, options)
			} else {
				process, err = execer.Start(ctx, *command)
			}
			if err != nil {
				return err
			}

			err = sendPID(ctx, process.Pid(), wsNetConn)
			if err != nil {
				return xerrors.Errorf("failed to send pid %d: %w", process.Pid(), err)
			}

			var outputgroup errgroup.Group
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stdout(), wsNetConn, proto.Header{Type: proto.TypeStdout})
			})
			outputgroup.Go(func() error {
				return copyWithHeader(process.Stderr(), wsNetConn, proto.Header{Type: proto.TypeStderr})
			})

			go func() {
				// Wait for the readers to close which happens when the connection
				// closes or the process dies.
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

// withSession runs the command in a session if screen is available.
func (srv *Server) withSession(ctx context.Context, id string, command *Command, execer Execer, options *Options) (Process, error) {
	// If screen is not installed spawn the command normally.
	_, err := exec.LookPath("screen")
	if err != nil {
		flog.Info("`screen` could not be found; session %s will not persist", id)
		return execer.Start(ctx, *command)
	}

	var s *Session
	srv.sessionsMutex.Lock()
	if rawSession, ok := srv.sessions.Load(id); ok {
		if s, ok = rawSession.(*Session); !ok {
			return nil, xerrors.Errorf("found invalid type in session map for ID %s", id)
		}
	} else {
		s = NewSession(command, execer, options)
		srv.sessions.Store(id, s)
		go func() { // Remove the session from the map once it closes.
			defer srv.sessions.Delete(id)
			s.Wait()
		}()
	}
	srv.sessionsMutex.Unlock()

	return s.Attach(ctx)
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
