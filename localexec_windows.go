// +build windows

package wsep

import (
	"context"
	"io"
	"os/exec"

	"golang.org/x/xerrors"
)

type localProcess struct {
	// tty may be nil
	tty uintptr
	cmd *exec.Cmd

	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (l *localProcess) Resize(_ context.Context, rows, cols uint16) error {
	return xerrors.Errorf("Windows local execution is not supported")
}

// Start executes the given command locally
func (l LocalExecer) Start(ctx context.Context, c Command) (Process, error) {
	return nil, xerrors.Errorf("Windows local execution is not supported")
}
