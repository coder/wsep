package wsep

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.coder.com/flog"
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
	// cond broadcasts session changes and any accompanying errors.
	cond *sync.Cond
	// configFile is the location of the screen configuration file.
	configFile string
	// error hold any error that occurred during a state change.  It is not safe
	// to access outside of cond.L.
	error error
	// execer is used to spawn the session and ready commands.
	execer Execer
	// id holds the id of the session for both creating and attaching.  This is
	// generated uniquely for each session (rather than using the ID provided by
	// the client) because without control of the daemon we do not have its PID
	// and without the PID screen will do partial matching.  Enforcing a UUID
	// should guarantee we match on the right session.
	id string
	// mutex prevents concurrent attaches to the session.  This is necessary since
	// screen will happily spawn two separate sessions with the same name if
	// multiple attaches happen in a close enough interval.  We are not able to
	// control the daemon ourselves to prevent this because the daemon will spawn
	// with a hardcoded 24x80 size which results in confusing padding above the
	// prompt once the attach comes in and resizes.
	mutex sync.Mutex
	// options holds options for configuring the session.
	options *Options
	// socketsDir is the location of the directory where screen should put its
	// sockets.
	socketsDir string
	// state holds the current session state.  It is not safe to access this
	// outside of cond.L.
	state State
	// timer will close the session when it expires.  The timer will be reset as
	// long as there are active connections.
	timer *time.Timer
}

const attachTimeout = 30 * time.Second

// NewSession sets up a new session.  Any errors with starting are returned on
// Attach().  The session will close itself if nothing is attached for the
// duration of the session timeout.
func NewSession(command *Command, execer Execer, options *Options) *Session {
	tempdir := filepath.Join(os.TempDir(), "coder-screen")
	s := &Session{
		command:    command,
		cond:       sync.NewCond(&sync.Mutex{}),
		configFile: filepath.Join(tempdir, "config"),
		execer:     execer,
		id:         uuid.NewString(),
		options:    options,
		state:      StateStarting,
		socketsDir: filepath.Join(tempdir, "sockets"),
	}
	go s.lifecycle()
	return s
}

// lifecycle manages the lifecycle of the session.
func (s *Session) lifecycle() {
	err := s.ensureSettings()
	if err != nil {
		s.setState(StateDone, xerrors.Errorf("ensure settings: %w", err))
		return
	}

	// The initial timeout for starting up is set here and will probably be far
	// shorter than the session timeout in most cases.  It should be at least long
	// enough for the first screen attach to be able to start up the daemon.
	s.timer = time.AfterFunc(attachTimeout, s.Close)

	s.setState(StateReady, nil)

	// Handle the close event by asking screen to quit the session.  We have no
	// way of knowing when the daemon process dies so the Go side will not get
	// cleaned up until the timeout if the process gets killed externally (for
	// example via `exit`).
	s.waitForState(StateClosing)
	s.timer.Stop()
	// If the command errors that the session is already gone that is fine.
	err = s.sendCommand(context.Background(), "quit", []string{"No screen session found"})
	if err != nil {
		flog.Error("failed to kill session %s: %v", s.id, err)
	}
	s.setState(StateDone, err)
}

// sendCommand runs a screen command against a session.  If the command fails
// with an error matching anything in successErrors it will be considered a
// success state (for example "no session" when quitting).  The command will be
// retried until successful, the timeout is reached, or the context ends (in
// which case the context error is returned).
func (s *Session) sendCommand(ctx context.Context, command string, successErrors []string) error {
	ctx, cancel := context.WithTimeout(ctx, attachTimeout)
	defer cancel()
	run := func() (bool, error) {
		process, err := s.execer.Start(ctx, Command{
			Command: "screen",
			Args:    []string{"-S", s.id, "-X", command},
			UID:     s.command.UID,
			GID:     s.command.GID,
			Env:     append(s.command.Env, "SCREENDIR="+s.socketsDir),
		})
		if err != nil {
			return true, err
		}
		stdout := captureStdout(process)
		err = process.Wait()
		// Try the context error in case it canceled while we waited.
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		details := <-stdout
		for _, se := range successErrors {
			if strings.Contains(details, se) {
				return true, nil
			}
		}
		// Sometimes a command will fail without any error output whatsoever but
		// will succeed later so all we can do is keep trying.
		return err == nil, nil
	}

	// Run immediately.
	if done, err := run(); done {
		return err
	}

	// Then run on a timer.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if done, err := run(); done {
				return err
			}
		}
	}
}

// Attach attaches to the session, waits for the attach to complete, then
// returns the attached process.
func (s *Session) Attach(ctx context.Context) (Process, error) {
	// We need to do this while behind the mutex to ensure another attach does not
	// come in and spawn a duplicate session.
	s.mutex.Lock()
	defer s.mutex.Unlock()

	state, err := s.waitForState(StateReady)
	switch state {
	case StateClosing:
		if err == nil {
			// No error means the session was closed by the user or timeout.
			err = xerrors.Errorf("session is closing")
		}
		return nil, err
	case StateDone:
		if err == nil {
			// No error means the daemon started successfully and was closed by the
			// user or timeout.
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

	// -S is for setting the session's name.
	// -x allows attaching to an already attached session.
	// -RR reattaches to the daemon or creates the session daemon if missing.
	// -q disables the "New screen..." message that appears for five seconds when
	// creating a new session with -RR.
	// -c is the flag for the config file.
	process, err := s.execer.Start(ctx, Command{
		Command:    "screen",
		Args:       append([]string{"-S", s.id, "-xRRqc", s.configFile, s.command.Command}, s.command.Args...),
		TTY:        s.command.TTY,
		Rows:       s.command.Rows,
		Cols:       s.command.Cols,
		Stdin:      s.command.Stdin,
		UID:        s.command.UID,
		GID:        s.command.GID,
		Env:        append(s.command.Env, "SCREENDIR="+s.socketsDir),
		WorkingDir: s.command.WorkingDir,
	})
	if err != nil {
		cancel()
		return nil, err
	}

	// Version seems to be the only command without a side effect so use it to
	// wait for the session to come up.
	err = s.sendCommand(ctx, "version", nil)
	if err != nil {
		cancel()
		return nil, err
	}

	return process, err
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

// captureStdout captures the first line of stdout.  Screen emits errors to
// stdout so this allows logging extra context beyond the exit code.
func captureStdout(process Process) <-chan string {
	stdout := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(process.Stdout())
		if scanner.Scan() {
			stdout <- scanner.Text()
		} else {
			stdout <- "no further details"
		}
	}()
	return stdout
}
