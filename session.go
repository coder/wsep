package wsep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"
)

// State represents the current state of the session.  States are sequential and
// will only move forward.
type State int

const (
	// StateStarting is the default/start state.
	StateStarting = iota
	// StateReady means the session is ready to be attached.
	StateReady
	// StateClosing means the session has begun closing.  The underlying process
	// may still be exiting.
	StateClosing
	// StateDone means the session has completely shut down and the process has
	// exited.
	StateDone
)

// Session represents a `screen` session.
type Session struct {
	// command is the original command used to spawn the session.
	command *Command
	// cond broadcasts session changes.
	cond *sync.Cond
	// configFile is the location of the screen configuration file.
	configFile string
	// error hold any error that occurred during a state change.
	error error
	// execer is used to spawn the session and ready commands.
	execer Execer
	// id holds the id of the session for both creating and attaching.
	id string
	// options holds options for configuring the session.
	options *Options
	// socketsDir is the location of the directory where screen should put its
	// sockets.
	socketsDir string
	// state holds the current session state.
	state State
	// timer will close the session when it expires.
	timer *time.Timer
}

// NewSession creates and immediately starts a new session.  Any errors with
// starting are returned on Attach().  The session will close itself if nothing
// is attached for the duration of the session timeout.
func NewSession(id string, command *Command, execer Execer, options *Options) *Session {
	tempdir := filepath.Join(os.TempDir(), "coder-screen")
	s := &Session{
		command:    command,
		cond:       sync.NewCond(&sync.Mutex{}),
		configFile: filepath.Join(tempdir, "config"),
		execer:     execer,
		options:    options,
		state:      StateStarting,
		socketsDir: filepath.Join(tempdir, "sockets"),
	}
	go s.lifecycle(id)
	return s
}

// lifecycle manages the lifecycle of the session.
func (s *Session) lifecycle(id string) {
	// When this context is done the process is dead.
	ctx, cancel := context.WithCancel(context.Background())

	// Close the session down immediately if there was an error.
	process, err := s.start(ctx, id)
	if err != nil {
		defer cancel()
		s.setState(StateDone, xerrors.Errorf("process start: %w", err))
		return
	}

	// Close the session after a timeout.  Timeouts created with AfterFunc can be
	// reset; we will reset it as long as there are active connections.  The
	// initial timeout for starting up is set here and will probably be less than
	// the session timeout in most cases.  It should be at least long enough for
	// screen to be able to start up.
	s.timer = time.AfterFunc(30*time.Second, s.Close)

	// Emit the done event when the process exits.
	go func() {
		defer cancel()
		err := process.Wait()
		if err != nil {
			err = xerrors.Errorf("process exit: %w", err)
		}
		s.setState(StateDone, err)
	}()

	// Handle the close event by killing the process.
	go func() {
		s.waitForState(StateClosing)
		s.timer.Stop()
		// process.Close() will send a SIGTERM allowing screen to clean up.  If we
		// kill it abruptly the socket will be left behind which is probably
		// harmless but it causes screen -list to show a bunch of dead sessions.
		// The user would likely not see this since we have a custom socket
		// directory but it seems ideal to let screen clean up anyway.
		process.Close()
		select {
		case <-ctx.Done():
			return // Process exited on its own.
		case <-time.After(5 * time.Second):
			// Still running; cancel the context to forcefully terminate the process.
			cancel()
		}
	}()

	// Wait until the session is ready to receive attaches.
	err = s.waitReady(ctx)
	if err != nil {
		defer cancel()
		s.setState(StateClosing, xerrors.Errorf("session wait: %w", err))
		return
	}

	// Once the session is ready external callers have until their provided
	// timeout to attach something before the session will close itself.
	s.timer.Reset(s.options.SessionTimeout)
	s.setState(StateReady, nil)
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

// waitReady waits for the session to be ready by running a command against the
// session until it works since sometimes if you attach too quickly after
// spawning screen it will say the session does not exist.  If the provided
// context finishes the wait is aborted and the context's error is returned.
func (s *Session) waitReady(ctx context.Context) error {
	check := func() (bool, error) {
		// The `version` command seems to be the only command without a side effect
		// so use it to check whether the session is up.
		process, err := s.execer.Start(ctx, Command{
			Command: "screen",
			Args:    []string{"-S", s.id, "-X", "version"},
			UID:     s.command.UID,
			GID:     s.command.GID,
			Env:     append(s.command.Env, "SCREENDIR="+s.socketsDir),
		})
		if err != nil {
			return true, err
		}
		err = process.Wait()
		// Try the context error in case it canceled while we waited.
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		// Session is ready once we executed without an error.
		// TODO: Should we specifically check for "no screen to be attached
		// matching"?  That might be the only error we actually want to retry and
		// otherwise we return immediately.  But this text may not be stable between
		// screen versions or there could be additional errors that can be retried.
		return err == nil, nil
	}

	// Check immediately.
	if done, err := check(); done {
		return err
	}

	// Then check on a timer.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if done, err := check(); done {
				return err
			}
		}
	}
}

// Attach waits for the session to become attachable and returns a command that
// can be used to attach to the session.
func (s *Session) Attach(ctx context.Context) (*Command, error) {
	state, err := s.waitForState(StateReady)
	switch state {
	case StateClosing:
		if err == nil {
			// No error means s.Close() was called, either by external code or via the
			// session timeout.
			err = xerrors.Errorf("session is closing")
		}
		return nil, err
	case StateDone:
		if err == nil {
			// No error means the process exited with zero, probably after being
			// killed due to a call to s.Close() either externally or via the session
			// timeout.
			err = xerrors.Errorf("session is done")
		}
		return nil, err
	}

	// Abort the heartbeat when the session closes.
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		s.waitForStateOrContext(ctx, StateClosing)
	}()

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
	// We just connected so reset the timer now in case it is near the end.
	s.timer.Reset(s.options.SessionTimeout)

	// Reset when the connection closes to ensure the session stays up for the
	// full timeout.
	defer s.timer.Reset(s.options.SessionTimeout)

	heartbeat := time.NewTicker(s.options.SessionTimeout / 2)
	defer heartbeat.Stop()

	for {
		select {
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
	s.waitForState(StateClosing)
}

// Close attempts to gracefully kill the session's underlying process then waits
// for the process to exit.  If the session does not exit in a timely manner it
// forcefully kills the process.
func (s *Session) Close() {
	s.setState(StateClosing, nil)
	s.waitForState(StateDone)
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

// setState sets and broadcasts the provided state if it is greater than the
// current state and the error if one has not already been set.
func (s *Session) setState(state State, err error) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	// Cannot regress states (for example trying to close after the process is
	// done should leave us in the done state and not the closing state).
	if state <= s.state {
		return
	}
	// Keep the first error we get.
	if s.error == nil {
		s.error = err
	}
	s.state = state
	s.cond.Broadcast()
}

// waitForState blocks until the state or a greater one is reached.
func (s *Session) waitForState(state State) (State, error) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	for state > s.state {
		s.cond.Wait()
	}
	return s.state, s.error
}

// waitForStateOrContext blocks until the state or a greater one is reached or
// the provided context ends.  If the context ends all goroutines will be woken.
func (s *Session) waitForStateOrContext(ctx context.Context, state State) {
	go func() {
		// Wake up when the context ends.
		defer s.cond.Broadcast()
		<-ctx.Done()
	}()
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	for ctx.Err() == nil && state > s.state {
		s.cond.Wait()
	}
}
