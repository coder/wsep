package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.coder.com/flog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"

	"cdr.dev/wsep/internal/proto"
)

var sessions sync.Map

// Options allows configuring the server.
type Options struct {
	SessionTimeout time.Duration
}

type session struct {
	ctx      context.Context
	screenID string
	ready    chan error
	timeout  *time.Timer
}

// Dispose closes the specified session.
func Dispose(id string) {
	if rawSession, ok := sessions.Load(id); ok {
		if sess, ok := rawSession.(*session); ok {
			sess.timeout.Reset(0)
			select {
			case <-sess.ctx.Done():
			}
		}
	}
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
	defer wsNetConn.Close()
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

			// Only TTYs with IDs can be reconnected.
			if command.TTY && header.ID != "" {
				command, err = createSession(ctx, header.ID, command, execer, options)
				if err != nil {
					return err
				}
			}

			// The process will get killed when the connection context ends.
			process, err = execer.Start(ctx, *command)
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

func createSession(ctx context.Context, id string, command *Command, execer Execer, options *Options) (*Command, error) {
	// If screen is not installed spawn the command normally.
	_, err := exec.LookPath("screen")
	if err != nil {
		flog.Info("`screen` could not be found; session %s will not persist", id)
		return command, nil
	}

	settings := []string{
		// Tell screen not to handle motion for xterm* terminals which
		// allows scrolling the terminal via the mouse wheel or scroll bar
		// (by default screen uses it to cycle through the command history).
		// There does not seem to be a way to make screen itself scroll on
		// mouse wheel.  tmux can do it but then there is no scroll bar and
		// it kicks you into copy mode where keys stop working until you
		// exit copy mode which seems like it could be confusing.
		"termcapinfo xterm* ti@:te@",
		// Enable alternate screen emulation otherwise applications get
		// rendered in the current window which wipes out visible output
		// resulting in missing output when scrolling back with the mouse
		// wheel (copy mode still works since that is screen itself
		// scrolling).
		"altscreen on",
		// Remap the control key to C-s since C-a may be used in
		// applications.  C-s cannot actually be used anyway since by
		// default it will pause and C-q to resume will just kill the
		// browser window.  We may not want people using the control key
		// anyway since it will not be obvious they are in screen and doing
		// things like switching windows makes mouse wheel scroll wonky due
		// to the terminal doing the scrolling rather than screen itself
		// (but again copy mode will work just fine).
		"escape ^Ss",
	}

	dir := filepath.Join(os.TempDir(), "coder-screen")
	config := filepath.Join(dir, "config")
	screendir := filepath.Join(dir, "sockets")

	err = os.MkdirAll(screendir, 0o700)
	if err != nil {
		return nil, xerrors.Errorf("unable to create %s for session %s: %w", screendir, id, err)
	}

	err = os.WriteFile(config, []byte(strings.Join(settings, "\n")), 0o644)
	if err != nil {
		return nil, xerrors.Errorf("unable to create %s for session %s: %w", config, id, err)
	}

	var sess *session
	if rawSession, ok := sessions.Load(id); ok {
		if sess, ok = rawSession.(*session); !ok {
			return nil, xerrors.Errorf("found invalid type in session map for ID %s", id)
		}
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		sess = &session{
			ctx:   ctx,
			ready: make(chan error, 1),
		}
		sessions.Store(id, sess)

		// Starting screen with -Dm causes it to launch a server tied to this
		// process, letting us attach to and kill it with the PID.
		process, err := execer.Start(ctx, Command{
			Command:    "screen",
			Args:       append([]string{"-S", id, "-Dmc", config, command.Command}, command.Args...),
			UID:        command.UID,
			GID:        command.GID,
			Env:        append(command.Env, "SCREENDIR="+screendir),
			WorkingDir: command.WorkingDir,
		})
		if err != nil {
			cancel()
			err = xerrors.Errorf("failed to create session %s: %w", id, err)
			sess.ready <- err
			close(sess.ready)
			return nil, err
		}

		// screenID will be used to attach to the session that was just created.
		// Screen allows attaching to a session via either the session name or
		// <pid>.<session-name>.  Use the latter form to differentiate between
		// sessions with the same name (for example if a session closes due to a
		// timeout and a client reconnects around the same time there can be two
		// sessions with the same name while the old session cleans up).
		sess.screenID = fmt.Sprintf("%d.%s", process.Pid(), id)

		// Timeouts created with AfterFunc can be reset.
		sess.timeout = time.AfterFunc(options.SessionTimeout, func() {
			// Delete immediately in case it takes a while to clean up otherwise new
			// connections with this ID will try to connect to the closing screen.
			sessions.Delete(id)
			// Close() will send a SIGTERM allowing screen to clean up.  If we kill it
			// abruptly the socket will be left behind which is probably harmless but
			// it causes screen -list to show a bunch of dead sessions (of course the
			// user would not see this since we have a custom socket directory) and it
			// seems ideal to let it clean up anyway.
			process.Close()
			select {
			case <-sess.ctx.Done():
				return
			case <-time.After(5 * time.Second):
				cancel() // Force screen to exit.
			}
		})

		// Remove the session if screen exits.
		go func() {
			err := process.Wait()
			// Screen exits with a one when it gets a SIGTERM.
			if exitErr, ok := err.(ExitError); ok && exitErr.ExitCode() != 1 {
				flog.Error("session %s exited with error %w", id, err)
			}
			cancel()
			sess.timeout.Stop()
			sessions.Delete(id)
		}()

		// Sometimes if you attach too quickly after spawning screen it will say the
		// session does not exist so run a command against it until it works.
		go func() {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			defer close(sess.ready)
			for {
				select {
				case <-sess.ctx.Done():
					sess.ready <- xerrors.Errorf("session %s is gone", id)
					return
				case <-ctx.Done():
					sess.ready <- xerrors.Errorf("timed out waiting for session %s", id)
					return
				default:
					process, err := execer.Start(ctx, Command{
						Command: "screen",
						Args:    []string{"-S", sess.screenID, "-X", "version"},
						UID:     command.UID,
						GID:     command.GID,
						Env:     append(command.Env, "SCREENDIR="+screendir),
					})
					if err != nil {
						sess.ready <- xerrors.Errorf("error waiting for session %s: %w", id, err)
						return
					}
					err = process.Wait()
					// TODO: Send error if it is anything but "no screen session found".
					if err == nil {
						return
					}
					time.Sleep(250 * time.Millisecond)
				}
			}
		}()
	}

	// Block until the server is ready.
	select {
	case err := <-sess.ready:
		if err != nil {
			return nil, err
		}
	}

	// Refresh the session timeout now.
	sess.timeout.Reset(options.SessionTimeout)

	// Keep refreshing the session timeout while this connection is alive.
	heartbeat := time.NewTicker(options.SessionTimeout / 2)
	go func() {
		defer heartbeat.Stop()
		// Reset when the connection closes to ensure the session stays up for the
		// full timeout.
		defer sess.timeout.Reset(options.SessionTimeout)
		for {
			select {
			// Stop looping once this request finishes.
			case <-ctx.Done():
				return
			case <-heartbeat.C:
			}
			sess.timeout.Reset(options.SessionTimeout)
		}
	}()

	// Use screen to connect to the session.
	return &Command{
		Command:    "screen",
		Args:       []string{"-S", sess.screenID, "-xc", config},
		TTY:        command.TTY,
		Rows:       command.Rows,
		Cols:       command.Cols,
		Stdin:      command.Stdin,
		UID:        command.UID,
		GID:        command.GID,
		Env:        append(command.Env, "SCREENDIR="+screendir),
		WorkingDir: command.WorkingDir,
	}, nil
}
