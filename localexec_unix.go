//go:build !windows
// +build !windows

package wsep

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"github.com/liamg/darktile/pkg/headless"
	"golang.org/x/xerrors"
)

type localProcess struct {
	// terminal may be nil
	terminal *headless.Terminal
	cmd      *exec.Cmd

	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (l *localProcess) Replay() string {
	if l.terminal == nil {
		return ""
	}
	return l.terminal.Serialize()
}

func (l *localProcess) Resize(_ context.Context, rows, cols uint16) error {
	if l.terminal == nil && rows != 0 && cols != 0 {
		return nil
	}
	return l.terminal.SetSize(rows, cols)
}

// Start executes the given command locally
func (l LocalExecer) Start(ctx context.Context, c Command) (Process, error) {
	var (
		process localProcess
		err     error
	)
	process.cmd = exec.CommandContext(ctx, c.Command, c.Args...)
	process.cmd.Env = append(os.Environ(), c.Env...)
	process.cmd.Dir = c.WorkingDir

	if c.GID != 0 || c.UID != 0 {
		process.cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{},
		}
	}
	if c.GID != 0 {
		process.cmd.SysProcAttr.Credential.Gid = c.GID
	}
	if c.UID != 0 {
		process.cmd.SysProcAttr.Credential.Uid = c.UID
	}

	if c.TTY {
		// This special WSEP_TTY variable helps debug unexpected TTYs.
		process.cmd.Env = append(process.cmd.Env, "WSEP_TTY=true")
		tty, err := pty.Start(process.cmd)
		if err != nil {
			return nil, xerrors.Errorf("start command with pty: %w", err)
		}

		// Scrollback is only necessary if there is an ID for reconnection.
		if c.ID != "" {
			// Start a terminal for parsing scrollback.
			process.terminal = headless.New()
			process.terminal.Run()
			process.stdout = io.TeeReader(tty, process.terminal)
		} else {
			process.stdout = tty
		}

		process.stderr = ioutil.NopCloser(bytes.NewReader(nil))
		process.stdin = tty
	} else {
		if c.Stdin {
			process.stdin, err = process.cmd.StdinPipe()
			if err != nil {
				return nil, xerrors.Errorf("create pipe: %w", err)
			}
		} else {
			process.stdin = disabledStdinWriter{}
		}

		process.stdout, err = process.cmd.StdoutPipe()
		if err != nil {
			return nil, xerrors.Errorf("create pipe: %w", err)
		}

		process.stderr, err = process.cmd.StderrPipe()
		if err != nil {
			return nil, xerrors.Errorf("create pipe: %w", err)
		}

		err = process.cmd.Start()
		if err != nil {
			return nil, xerrors.Errorf("start command: %w", err)
		}
	}

	if l.ChildProcessPriority != nil {
		pid := process.cmd.Process.Pid
		niceness := *l.ChildProcessPriority

		// the environment may block the niceness syscall
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, niceness)
	}

	return &process, nil
}
