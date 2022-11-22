package wsep

import (
	"context"
	"io"

	"cdr.dev/wsep/internal/proto"
)

// ExitError is sent when the command terminates.
type ExitError struct {
	code  int
	error string
}

// ExitCode returns the exit code of the process.
func (e ExitError) ExitCode() int {
	return e.code
}

// Error returns a string describing why the process errored.
func (e ExitError) Error() string {
	return e.error
}

// Process represents a started command.
type Process interface {
	// Pid is populated immediately during a successful start with the process ID.
	Pid() int
	// Stdout returns an io.WriteCloser that will pipe writes to the remote command.
	// Closure of stdin sends the corresponding close message.
	Stdin() io.WriteCloser
	// Stdout returns an io.Reader that is connected to the command's standard output.
	Stdout() io.Reader
	// Stderr returns an io.Reader that is connected to the command's standard error.
	Stderr() io.Reader
	// Resize resizes the TTY if a TTY is enabled.
	Resize(ctx context.Context, rows, cols uint16) error
	// Wait returns ExitError when the command terminates with a non-zero exit code.
	Wait() error
	// Close sends a SIGTERM to the process.  To force a shutdown cancel the
	// context passed into the execer.
	Close() error
}

// Execer starts commands.
type Execer interface {
	Start(ctx context.Context, c Command) (Process, error)
}

// theses maps are needed to prevent an import cycle
func mapToProtoCmd(c Command) proto.Command {
	return proto.Command{
		Command:    c.Command,
		Args:       c.Args,
		Stdin:      c.Stdin,
		TTY:        c.TTY,
		Rows:       c.Rows,
		Cols:       c.Cols,
		UID:        c.UID,
		GID:        c.GID,
		Env:        c.Env,
		WorkingDir: c.WorkingDir,
	}
}

func mapToClientCmd(c proto.Command) *Command {
	return &Command{
		Command:    c.Command,
		Args:       c.Args,
		Stdin:      c.Stdin,
		TTY:        c.TTY,
		Rows:       c.Rows,
		Cols:       c.Cols,
		UID:        c.UID,
		GID:        c.GID,
		Env:        c.Env,
		WorkingDir: c.WorkingDir,
	}
}
