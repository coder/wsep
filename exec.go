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
	Pid() int
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	// Resize resizes the TTY if a TTY is enabled.
	Resize(ctx context.Context, rows, cols uint16) error
	// Wait returns ExitError when the command terminates.
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
		TTY:        c.TTY,
		UID:        c.UID,
		GID:        c.GID,
		Env:        c.Env,
		WorkingDir: c.WorkingDir,
	}
}

func mapToClientCmd(c proto.Command) Command {
	return Command{
		Command:    c.Command,
		Args:       c.Args,
		TTY:        c.TTY,
		UID:        c.UID,
		GID:        c.GID,
		Env:        c.Env,
		WorkingDir: c.WorkingDir,
	}
}
