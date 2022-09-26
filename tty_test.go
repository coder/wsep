package wsep

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"cdr.dev/slog/sloggers/slogtest/assert"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
)

func TestTTY(t *testing.T) {
	t.Parallel()

	// Run some output in a new session.
	server := newServer(t)
	ctx, command := newSession(t)
	command.ID = "" // No ID so we do not start a reconnectable session.
	process, _ := connect(ctx, t, command, server, nil)
	expected := writeUnique(t, process)
	assert.True(t, "find initial output", checkStdout(t, process, expected, []string{}))

	// Connect to the same session.  There should not be shared output since
	// these end up being separate sessions due to the lack of an ID.
	process, _ = connect(ctx, t, command, server, nil)
	unexpected := expected
	expected = writeUnique(t, process)
	assert.True(t, "find new session output", checkStdout(t, process, expected, unexpected))
}

func TestReconnectTTY(t *testing.T) {
	t.Run("DeprecatedServe", func(t *testing.T) {
		// Do something in the first session.
		ctx, command := newSession(t)
		process, _ := connect(ctx, t, command, nil, nil)
		expected := writeUnique(t, process)
		assert.True(t, "find initial output", checkStdout(t, process, expected, []string{}))

		// Connect to the same session.  Should see the same output.
		process, _ = connect(ctx, t, command, nil, nil)
		assert.True(t, "find reconnected output", checkStdout(t, process, expected, []string{}))
	})

	t.Run("NoScreen", func(t *testing.T) {
		t.Setenv("PATH", "/bin")

		// Run some output in a new session.
		server := newServer(t)
		ctx, command := newSession(t)
		process, _ := connect(ctx, t, command, server, nil)
		expected := writeUnique(t, process)
		assert.True(t, "find initial output", checkStdout(t, process, expected, []string{}))

		// Connect to the same session.  There should not be shared output since
		// these end up being separate sessions due to the lack of screen.
		process, _ = connect(ctx, t, command, server, nil)
		unexpected := expected
		expected = writeUnique(t, process)
		assert.True(t, "find new session output", checkStdout(t, process, expected, unexpected))
	})

	t.Run("Regular", func(t *testing.T) {
		t.Parallel()

		// Run some output in a new session.
		server := newServer(t)
		ctx, command := newSession(t)
		process, disconnect := connect(ctx, t, command, server, nil)
		expected := writeUnique(t, process)
		assert.True(t, "find initial output", checkStdout(t, process, expected, []string{}))

		// Reconnect and sleep; the inactivity timeout should not trigger since we
		// were not disconnected during the timeout.
		disconnect()
		process, disconnect = connect(ctx, t, command, server, nil)
		time.Sleep(time.Second)
		expected = append(expected, writeUnique(t, process)...)
		assert.True(t, "find reconnected output", checkStdout(t, process, expected, []string{}))

		// Make a simultaneously active connection.
		process2, disconnect2 := connect(ctx, t, command, server, &Options{
			// Divide the time to test that the heartbeat keeps it open through
			// multiple intervals.
			SessionTimeout: time.Second / 4,
		})

		// Disconnect the first connection and wait for inactivity.  The session
		// should stay up because of the second connection.
		disconnect()
		time.Sleep(time.Second)
		expected = append(expected, writeUnique(t, process2)...)
		assert.True(t, "find second connection output", checkStdout(t, process2, expected, []string{}))

		// Disconnect the second connection and wait for inactivity.  The next
		// connection should start a new session so we should only see new output
		// and not any output from the old session.
		disconnect2()
		time.Sleep(time.Second)
		process, disconnect = connect(ctx, t, command, server, nil)
		unexpected := expected
		expected = writeUnique(t, process)
		assert.True(t, "find new session output", checkStdout(t, process, expected, unexpected))
	})

	t.Run("Alternate", func(t *testing.T) {
		t.Parallel()

		// Run an application that enters the alternate screen.
		server := newServer(t)
		ctx, command := newSession(t)
		process, disconnect := connect(ctx, t, command, server, nil)
		write(t, process, "./ci/alt.sh")
		assert.True(t, "find alt screen", checkStdout(t, process, []string{"./ci/alt.sh", "ALT SCREEN"}, []string{}))

		// Reconnect; the application should redraw.  We should have only the
		// application output and not the command that spawned the application.
		disconnect()
		process, disconnect = connect(ctx, t, command, server, nil)
		assert.True(t, "find reconnected alt screen", checkStdout(t, process, []string{"ALT SCREEN"}, []string{"./ci/alt.sh"}))

		// Exit the application and reconnect.  Should now be in a regular shell.
		write(t, process, "q")
		disconnect()
		process, _ = connect(ctx, t, command, server, nil)
		expected := writeUnique(t, process)
		assert.True(t, "find shell output", checkStdout(t, process, expected, []string{}))
	})

	t.Run("Simultaneous", func(t *testing.T) {
		t.Parallel()

		server := newServer(t)

		// Try connecting a bunch of sessions at once.
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, command := newSession(t)
				process, disconnect := connect(ctx, t, command, server, nil)
				expected := writeUnique(t, process)
				assert.True(t, "find initial output", checkStdout(t, process, expected, []string{}))

				n := rand.Intn(1000)
				time.Sleep(time.Duration(n) * time.Millisecond)
				disconnect()
				process, disconnect = connect(ctx, t, command, server, nil)
				expected = append(expected, writeUnique(t, process)...)
				assert.True(t, "find reconnected output", checkStdout(t, process, expected, []string{}))
			}()
		}
		wg.Wait()
	})
}

// newServer returns a new wsep server.
func newServer(t *testing.T) *Server {
	server := NewServer()
	t.Cleanup(func() {
		server.Close()
		assert.Equal(t, "no leaked sessions", 0, server.SessionCount())
	})
	return server
}

// newSession returns a command for starting/attaching to a session with a
// context for timing out.
func newSession(t *testing.T) (context.Context, Command) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	command := Command{
		ID:      uuid.NewString(),
		Command: "sh",
		TTY:     true,
		Stdin:   true,
		Cols:    100,
		Rows:    100,
		Env:     []string{"TERM=xterm"},
	}

	return ctx, command
}

// connect connects to a wsep server and runs the provided command.
func connect(ctx context.Context, t *testing.T, command Command, wsepServer *Server, options *Options) (Process, func()) {
	if options == nil {
		options = &Options{SessionTimeout: time.Second}
	}
	ws, server := mockConn(ctx, t, wsepServer, options)
	t.Cleanup(func() {
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()
	})

	process, err := RemoteExecer(ws).Start(ctx, command)
	assert.Success(t, "start sh", err)

	return process, func() {
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()
	}
}

// writeUnique writes some unique output to the shell process and returns the
// expected output.
func writeUnique(t *testing.T, process Process) []string {
	n := rand.Intn(1000000)
	echoCmd := fmt.Sprintf("echo test:$((%d+%d))", n, n)
	write(t, process, echoCmd)
	return []string{echoCmd, fmt.Sprintf("test:%d", n+n)}
}

// write writes the provided input followed by a newline to the shell process.
func write(t *testing.T, process Process, input string) {
	_, err := process.Stdin().Write([]byte(input + "\n"))
	assert.Success(t, "write to stdin", err)
}

// checkStdout ensures that expected is in the stdout in the specified order.
// On the way if anything in unexpected comes up return false.  Return once
// everything in expected has been found or EOF.
func checkStdout(t *testing.T, process Process, expected, unexpected []string) bool {
	t.Helper()
	i := 0
	scanner := bufio.NewScanner(process.Stdout())
	for scanner.Scan() {
		line := scanner.Text()
		t.Logf("bash tty stdout = %s", strings.ReplaceAll(line, "\x1b", "ESC"))
		for _, str := range unexpected {
			if strings.Contains(line, str) {
				t.Logf("contains unexpected line %s", line)
				return false
			}
		}
		if strings.Contains(line, expected[i]) {
			t.Logf("contains expected line %s", line)
			i = i + 1
		}
		if i == len(expected) {
			return true
		}
	}
	return false
}
