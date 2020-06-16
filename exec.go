package wsep

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"cdr.dev/wsep/internal/proto"
	"golang.org/x/xerrors"
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
	// Pid is populated immediately during a successfull start with the process ID.
	Pid() int
	// Stdout returns an io.WriteCloser that will pipe writes to the remote command.
	// Closure of stdin sends the correspoding close messsage.
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

type syncWriter struct {
	W  io.Writer
	mu sync.Mutex
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.W.Write(p)
}

// CombinedOutput runs the command through the execer combining
// the output from stderr and stdout. If err != nil then out will
// have any error output from the command.
// The function makes no effort to limit output.
func CombinedOutput(ctx context.Context, execer Execer, c Command) (out []byte, err error) {
	proc, err := execer.Start(ctx, c)
	if err != nil {
		return nil, xerrors.Errorf("start execer: %w", err)
	}

	var (
		buf bytes.Buffer
		sw  = &syncWriter{
			W: &buf,
		}
	)

	go io.Copy(sw, proc.Stderr())
	go io.Copy(sw, proc.Stdout())

	err = proc.Wait()
	return buf.Bytes(), err
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

func mapToClientCmd(c proto.Command) Command {
	return Command{
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
