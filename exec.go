package wsep

import (
	"context"
	"fmt"
	"io"

	"cdr.dev/wsep/internal/proto"
)

// ExitError is sent when the command terminates.
type ExitError struct {
	Code int
}

func (e ExitError) Error() string {
	return fmt.Sprintf("process exited with code %v", e.Code)
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
	// Close terminates the process and underlying connection(s).
	// It must be called otherwise a connection or process may leak.
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
		UID:        c.UID,
		GID:        c.GID,
		Env:        c.Env,
		WorkingDir: c.WorkingDir,
	}
}

func mapToClientCmd(c proto.ClientStartHeader) Command {
	return Command{
		ID:         c.ID,
		Rows:       c.Rows,
		Cols:       c.Cols,
		Command:    c.Command.Command,
		Args:       c.Command.Args,
		Stdin:      c.Command.Stdin,
		TTY:        c.Command.TTY,
		UID:        c.Command.UID,
		GID:        c.Command.GID,
		Env:        c.Command.Env,
		WorkingDir: c.Command.WorkingDir,
	}
}
