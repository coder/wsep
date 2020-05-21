package wsep

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"

	"cdr.dev/wsep/internal/proto"
	"github.com/creack/pty"
	"golang.org/x/xerrors"
)

// LocalExecer executes command on the local system.
type LocalExecer struct {
}

type localProcess struct {
	// tty may be nil
	tty *os.File
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (l *localProcess) Stdin() io.WriteCloser {
	return l.stdin
}

func (l *localProcess) Stdout() io.Reader {
	return l.stdout
}

func (l *localProcess) Stderr() io.Reader {
	return l.stderr
}

func (l *localProcess) Wait() error {
	err := l.cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &ExitError{
			Code: exitErr.ExitCode(),
		}
	}
	return err
}

func (l *localProcess) Close() error {
	return l.cmd.Process.Kill()
}

func (l *localProcess) Resize(rows, cols uint16) error {
	if l.tty == nil {
		return nil
	}
	return pty.Setsize(l.tty, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (l *localProcess) Pid() int {
	return l.cmd.Process.Pid
}

func (l LocalExecer) Start(ctx context.Context, c proto.Command) (Process, error) {
	var (
		process localProcess
	)
	process.cmd = exec.Command(c.Command, c.Args...)
	process.cmd.Env = c.Env
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
		process.cmd.SysProcAttr.Credential.Gid = c.UID
	}

	var (
		err error
	)

	process.stdin, err = process.cmd.StdinPipe()
	if err != nil {
		return nil, xerrors.Errorf("create pipe: %w", err)
	}

	process.stdout, err = process.cmd.StdoutPipe()
	if err != nil {
		return nil, xerrors.Errorf("create pipe: %w", err)
	}

	process.stderr, err = process.cmd.StderrPipe()
	if err != nil {
		return nil, xerrors.Errorf("create pipe: %w", err)
	}

	if c.TTY {
		process.tty, err = pty.Start(process.cmd)
		if err != nil {
			return nil, xerrors.Errorf("start command with pty: %w", err)
		}
	} else {
		err = process.cmd.Start()
		if err != nil {
			return nil, xerrors.Errorf("start command: %w", err)
		}
	}

	return &process, nil
}
