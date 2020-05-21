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
	Resize(rows, cols uint16) error
	// Wait returns ExitError when the command terminates.
	Wait() error
	// Close terminates the process and underlying connection(s).
	// It must be called otherwise a connection or process may leak.
	Close() error
}

// Execer starts commands.
type Execer interface {
	Start(ctx context.Context, c proto.Command) (Process, error)
}
