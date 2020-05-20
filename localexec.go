package wsep

import (
	"context"
	"io"
	"os"
	"os/exec"

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
	return l.cmd.Wait()
}

func (l *localProcess) Close() error {
	return l.cmd.Process.Kill()
}

func (l *localProcess) Pid() int {
	return l.cmd.Process.Pid
}

func (l LocalExecer) Start(ctx context.Context, c Command) (Process, error) {
	var (
		process localProcess
	)
	process.cmd = exec.Command(c.Command, c.Args...)

	var (
		err     error
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
