// +build windows

package wsep

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/iamacarpet/go-winpty"
	"golang.org/x/xerrors"
)

type localProcess struct {
	// tty may be nil
	tty *winpty.WinPTY
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (l *localProcess) Resize(_ context.Context, rows, cols uint16) error {
	if l.tty == nil {
		return nil
	}

	l.tty.SetSize(uint32(cols), uint32(rows))
	return nil
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

	if c.TTY {
		process.tty, err = winpty.OpenWithOptions(winpty.Options{
			DLLPrefix: "winpty",
			Command:   c.Command + " " + strings.Join(c.Args, " "),
			Dir:       c.WorkingDir,

			// This special WSEP_TTY variable helps debug unexpected TTYs.
			Env: append(process.cmd.Env, "WSEP_TTY=true"),
		})
		if err != nil {
			return nil, xerrors.Errorf("start command with pty: %w", err)
		}
		process.stdout = process.tty.StdOut
		process.stderr = ioutil.NopCloser(bytes.NewReader(nil))
		process.stdin = process.tty.StdIn
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

	return &process, nil
}
