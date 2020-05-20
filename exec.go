package wsep

import (
	"context"
	"io"
)

// Command represents a runnable command.
type Command struct {
	Command string
	Args    []string
	TTY     bool
}

// ExitError is sent when the command terminates.
type ExitError struct {
	Code int
}

// Process represents a started command.
type Process interface {
	Pid() int
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
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

