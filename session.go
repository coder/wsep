package wsep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// Session represents a `screen` session.
type Session struct {
	// close receives nil when the session should be closed.
	close chan struct{}
	// closing receives nil when the session has begun closing.  The underlying
	// process may still be exiting.  closing implies ready.
	closing chan struct{}
	// command is the original command used to spawn the session.
	command *Command
	// configFile is the location of the screen configuration file.
	configFile string
	// done receives nil when the session has completely shut down and the process
	// has exited.  done implies closing and ready.
	done chan struct{}
	// error hold any error that occurred while starting the session.
	error error
	// execer is used to spawn the session and ready commands.
	execer Execer
	// id holds the id of the session for both creating and attaching.
	id string
	// options holds options for configuring the session.
	options *Options
	// ready receives nil when the session is ready to be attached.  error must be
	// checked after receiving on this channel.
	ready chan struct{}
	// socketsDir is the location of the directory where screen should put its
	// sockets.
	socketsDir string
	// timer will close the session when it expires.
	timer *time.Timer
}

// NewSession creates and immediately starts a new session.  Any errors with
// starting are returned on Attach().  The session will close itself if nothing
// is attached for the duration of the session timeout.
func NewSession(id string, command *Command, execer Execer, options *Options) *Session {
	tempdir := filepath.Join(os.TempDir(), "coder-screen")
	s := &Session{
		close:      make(chan struct{}),
		closing:    make(chan struct{}),
		command:    command,
		configFile: filepath.Join(tempdir, "config"),
		done:       make(chan struct{}),
		execer:     execer,
		options:    options,
		ready:      make(chan struct{}),
		socketsDir: filepath.Join(tempdir, "sockets"),
	}
	go s.lifecycle(id)
	return s
}

// lifecycle manages the lifecycle of the session.
func (s *Session) lifecycle(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Close the session after a timeout.  Timeouts created with AfterFunc can be
	// reset; we will reset it as long as there are active connections.
	s.timer = time.AfterFunc(s.options.SessionTimeout, s.Close)
	defer s.timer.Stop()

	// Close the session down immediately if there was an error and store that
	// error to emit on Attach() calls.
	process, err := s.start(ctx, id)
	if err != nil {
		s.error = err
		close(s.ready)
		close(s.closing)
		close(s.done)
		return
	}

	// Mark the session as fully done when the process exits.
	go func() {
		err = process.Wait()
		if err != nil {
			s.error = err
		}
		close(s.done)
	}()

	// Wait until the session is ready to receive attaches.
	err = s.waitReady(ctx)
	if err != nil {
		s.error = err
		close(s.ready)
		close(s.closing)
		// The deferred cancel will kill the process if it is still running which
		// will then close the done channel.
		return
	}

	close(s.ready)

	select {
	// When the session is closed try gracefully killing the process.
	case <-s.close:
		// Mark the session as closing so you can use Wait() to stop attaching new
		// connections to sessions that are closing down.
		close(s.closing)
		// process.Close() will send a SIGTERM allowing screen to clean up.  If we
		// kill it abruptly the socket will be left behind which is probably
		// harmless but it causes screen -list to show a bunch of dead sessions.
		// The user would likely not see this since we have a custom socket
		// directory but it seems ideal to let screen clean up anyway.
		process.Close()
		select {
		case <-s.done:
			return // Process exited on its own.
		case <-time.After(5 * time.Second):
			// Still running; yield so we can run the deferred cancel which will
			// forcefully terminate the session.
			return
		}
	// If the process exits on its own the session is also considered closed.
	case <-s.done:
		close(s.closing)
	}
}

// start starts the session.
func (s *Session) start(ctx context.Context, id string) (Process, error) {
	err := s.ensureSettings()
	if err != nil {
		return nil, err
	}

	// -S is for setting the session's name.
	// -Dm causes screen to launch a server tied to this process, letting us
	//     attach to and kill it with the PID of this process (rather than having
	//     to do something flaky like run `screen -S id -quit`).
	// -c is the flag for the config file.
	process, err := s.execer.Start(ctx, Command{
		Command:    "screen",
		Args:       append([]string{"-S", id, "-Dmc", s.configFile, s.command.Command}, s.command.Args...),
		UID:        s.command.UID,
		GID:        s.command.GID,
		Env:        append(s.command.Env, "SCREENDIR="+s.socketsDir),
		WorkingDir: s.command.WorkingDir,
	})
	if err != nil {
		return nil, err
	}

	// Screen allows targeting sessions via either the session name or
	// <pid>.<session-name>.  Using the latter form allows us to differentiate
	// between sessions with the same name.  For example if a session closes due
	// to a timeout and a client reconnects around the same time there can be two
	// sessions with the same name while the old session is cleaning up.  This is
	// only a problem while attaching and not creating since the creation command
	// used always creates a new session.
	s.id = fmt.Sprintf("%d.%s", process.Pid(), id)
	return process, nil
}

// waitReady waits for the session to be ready.  Sometimes if you attach too
// quickly after spawning screen it will say the session does not exist so this
// will run a command against the session until it works or times out.
func (s *Session) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-s.close:
			return xerrors.Errorf("session has been closed")
		case <-s.done:
			return xerrors.Errorf("session has exited")
		case <-ctx.Done():
			return xerrors.Errorf("timed out waiting for session")
		default:
			// The `version` command seems to be the only command without a side
			// effect so use it to check whether the session is up.
			process, err := s.execer.Start(ctx, Command{
				Command: "screen",
				Args:    []string{"-S", s.id, "-X", "version"},
				UID:     s.command.UID,
				GID:     s.command.GID,
				Env:     append(s.command.Env, "SCREENDIR="+s.socketsDir),
			})
			if err != nil {
				return err
			}
			err = process.Wait()
			// TODO: Return error if it is anything but "no screen session found".
			if err == nil {
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// Attach waits for the session to become attachable and returns a command that
// can be used to attach to the session.
func (s *Session) Attach(ctx context.Context) (*Command, error) {
	<-s.ready
	if s.error != nil {
		return nil, s.error
	}

	go s.heartbeat(ctx)

	return &Command{
		Command:    "screen",
		Args:       []string{"-S", s.id, "-xc", s.configFile},
		TTY:        s.command.TTY,
		Rows:       s.command.Rows,
		Cols:       s.command.Cols,
		Stdin:      s.command.Stdin,
		UID:        s.command.UID,
		GID:        s.command.GID,
		Env:        append(s.command.Env, "SCREENDIR="+s.socketsDir),
		WorkingDir: s.command.WorkingDir,
	}, nil
}

// heartbeat keeps the session alive while the provided context is not done.
func (s *Session) heartbeat(ctx context.Context) {
	// We just connected so reset the timer now in case it is near the end or this
	// is the first connection and the timer has not been set yet.
	s.timer.Reset(s.options.SessionTimeout)

	// Reset when the connection closes to ensure the session stays up for the
	// full timeout.
	defer s.timer.Reset(s.options.SessionTimeout)

	heartbeat := time.NewTicker(s.options.SessionTimeout / 2)
	defer heartbeat.Stop()

	for {
		select {
		case <-s.closing:
			return
		case <-ctx.Done():
			return
		case <-heartbeat.C:
		}
		s.timer.Reset(s.options.SessionTimeout)
	}
}

// Wait waits for the session to close.  The underlying process might still be
// exiting.
func (s *Session) Wait() {
	<-s.closing
}

// Close attempts to gracefully kill the session's underlying process then waits
// for the process to exit.  If the session does not exit in a timely manner it
// forcefully kills the process.
func (s *Session) Close() {
	select {
	case s.close <- struct{}{}:
	default: // Do not block; the lifecycle has already completed.
	}
	<-s.done
}

// ensureSettings writes config settings and creates the socket directory.
func (s *Session) ensureSettings() error {
	settings := []string{
		// Tell screen not to handle motion for xterm* terminals which allows
		// scrolling the terminal via the mouse wheel or scroll bar (by default
		// screen uses it to cycle through the command history).  There does not
		// seem to be a way to make screen itself scroll on mouse wheel.  tmux can
		// do it but then there is no scroll bar and it kicks you into copy mode
		// where keys stop working until you exit copy mode which seems like it
		// could be confusing.
		"termcapinfo xterm* ti@:te@",
		// Enable alternate screen emulation otherwise applications get rendered in
		// the current window which wipes out visible output resulting in missing
		// output when scrolling back with the mouse wheel (copy mode still works
		// since that is screen itself scrolling).
		"altscreen on",
		// Remap the control key to C-s since C-a may be used in applications.  C-s
		// cannot actually be used anyway since by default it will pause and C-q to
		// resume will just kill the browser window.  We may not want people using
		// the control key anyway since it will not be obvious they are in screen
		// and doing things like switching windows makes mouse wheel scroll wonky
		// due to the terminal doing the scrolling rather than screen itself (but
		// again copy mode will work just fine).
		"escape ^Ss",
	}

	dir := filepath.Join(os.TempDir(), "coder-screen")
	config := filepath.Join(dir, "config")
	socketdir := filepath.Join(dir, "sockets")

	err := os.MkdirAll(socketdir, 0o700)
	if err != nil {
		return err
	}

	return os.WriteFile(config, []byte(strings.Join(settings, "\n")), 0o644)
}
