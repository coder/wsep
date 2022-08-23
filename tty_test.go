package wsep

import (
	"bufio"
	"context"
	"io/ioutil"
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws, server := mockConn(ctx, t, nil)
	defer ws.Close(websocket.StatusInternalError, "")
	defer server.Close()

	execer := RemoteExecer(ws)
	testTTY(ctx, t, execer)
}

func testTTY(ctx context.Context, t *testing.T, e Execer) {
	process, err := e.Start(ctx, Command{
		Command: "sh",
		TTY:     true,
		Stdin:   true,
	})
	assert.Success(t, "start sh", err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		stdout, err := ioutil.ReadAll(process.Stdout())
		assert.Success(t, "read stdout", err)

		t.Logf("bash tty stdout = %s", stdout)
		prompt := string(stdout)
		assert.True(t, `bash "$" or "#" prompt found`,
			strings.HasSuffix(prompt, "$ ") || strings.HasSuffix(prompt, "# "))
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()

		stderr, err := ioutil.ReadAll(process.Stderr())
		assert.Success(t, "read stderr", err)
		t.Logf("bash tty stderr = %s", stderr)
		assert.True(t, "stderr is empty", len(stderr) == 0)
	}()
	time.Sleep(3 * time.Second)

	process.Close()
	wg.Wait()
}

func TestReconnectTTY(t *testing.T) {
	t.Parallel()

	t.Run("RegularScreen", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		command := Command{
			ID:      uuid.NewString(),
			Command: "sh",
			TTY:     true,
			Stdin:   true,
		}

		ws, server := mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err := RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "start sh", err)

		// Write some unique output.
		echoCmd := "echo test:$((1+1))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected := []string{echoCmd, "test:2"}

		assert.True(t, "find echo", findStdout(t, process, expected))

		// Test disconnecting then reconnecting.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		ws, server = mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// The inactivity timeout should not have been triggered.
		time.Sleep(time.Second)

		echoCmd = "echo test:$((2+2))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected = append(expected, echoCmd, "test:4")

		assert.True(t, "find echo", findStdout(t, process, expected))

		// Test disconnecting while another connection is active.
		ws2, server2 := mockConn(ctx, t, &Options{
			// Divide the time to test that the heartbeat keeps it open through multiple
			// intervals.
			ReconnectingProcessTimeout: time.Second / 4,
		})
		defer server2.Close()

		process, err = RemoteExecer(ws2).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()
		time.Sleep(time.Second)

		// This connection should still be up.
		echoCmd = "echo test:$((3+3))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected = append(expected, echoCmd, "test:6")

		assert.True(t, "find echo", findStdout(t, process, expected))

		// Close the remaining connection and wait for inactivity.
		ws2.Close(websocket.StatusNormalClosure, "disconnected")
		server2.Close()
		time.Sleep(time.Second)

		// The next connection should start a new process.
		ws, server = mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// This time no echo since it is a new process.
		assert.True(t, "find echo", !findStdout(t, process, expected))
	})

	t.Run("AlternateScreen", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		command := Command{
			ID:      uuid.NewString(),
			Command: "sh",
			TTY:     true,
			Stdin:   true,
		}

		ws, server := mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err := RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// Run an application that enters the alternate screen.
		_, err = process.Stdin().Write([]byte("./ci/alt.sh\r\n"))
		assert.Success(t, "write to stdin", err)

		assert.True(t, "find output", findStdout(t, process, []string{"./ci/alt.sh", "ALT SCREEN"}))

		// Reconnect; the application should redraw.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		ws, server = mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		assert.True(t, "find output", findStdout(t, process, []string{"./ci/alt.sh", "ALT SCREEN"}))

		// Exit the application.
		_, err = process.Stdin().Write([]byte("q"))
		assert.Success(t, "write to stdin", err)

		// Reconnect; the regular buffer should display but not the application.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		ws, server = mockConn(ctx, t, &Options{
			ReconnectingProcessTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		assert.True(t, "find output", findStdout(t, process, []string{"./ci/alt.sh"}))
		assert.True(t, "find output", !findStdout(t, process, []string{"ALT SCREEN"}))
	})
}

func findStdout(t *testing.T, process Process, expected []string) bool {
	t.Helper()
	scanner := bufio.NewScanner(process.Stdout())
outer:
	for _, str := range expected {
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("bash tty stdout = %s", strings.ReplaceAll(line, "\x1b", "ESC"))
			if strings.Contains(line, str) {
				continue outer
			}
		}
		return false // Reached the end of output without finding str.
	}
	return true
}
