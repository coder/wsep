package wsep

import (
	"io"
	"os/exec"

	"golang.org/x/xerrors"
)

// LocalExecer executes command on the local system.
type LocalExecer struct {
	// ChildProcessPriority overrides the default niceness of all child processes launch by LocalExecer.
	ChildProcessPriority *int
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
		return ExitError{
			Code: exitErr.ExitCode(),
		}
	}
	return err
}

func (l *localProcess) Close() error {
	return l.cmd.Process.Kill()
}

func (l *localProcess) Pid() int {
	return l.cmd.Process.Pid
}

type disabledStdinWriter struct{}

func (w disabledStdinWriter) Close() error {
	return nil
}

func (w disabledStdinWriter) Write(_ []byte) (written int, err error) {
	return 0, xerrors.Errorf("stdin is not enabled for this command")
}
