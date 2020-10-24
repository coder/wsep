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
	"golang.org/x/xerrors"
)

type localProcess struct {
	// tty may be nil
	tty *os.File
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (l *localProcess) Resize(_ context.Context, rows, cols uint16) error {
	if l.tty == nil {
		return nil
	}
	return pty.Setsize(l.tty, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
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
		process.tty, err = pty.Start(process.cmd)
		if err != nil {
			return nil, xerrors.Errorf("start command with pty: %w", err)
		}
		process.stdout = process.tty
		process.stderr = ioutil.NopCloser(bytes.NewReader(nil))
		process.stdin = process.tty
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

		if l.ChildProcessPriority != nil {
			pid := process.cmd.Process.Pid
			niceness := *l.ChildProcessPriority
			err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, niceness)
			if err != nil {
				return nil, xerrors.Errorf("set process (pid: %d) priority to (niceness: %d): %w", pid, niceness, err)
			}
		}
	}

	return &process, nil
}
