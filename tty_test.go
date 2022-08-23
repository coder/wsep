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
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err := RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "start sh", err)

		// Write some unique output.
		echoCmd := "echo test:$((1+1))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected := []string{echoCmd, "test:2"}

		assert.True(t, "find echo", checkStdout(t, process, expected, []string{}))

		// Disconnect.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		// Reconnect.
		ws, server = mockConn(ctx, t, &Options{
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// The inactivity timeout should not trigger since we are connected.
		time.Sleep(time.Second)

		echoCmd = "echo test:$((2+2))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected = append(expected, echoCmd, "test:4")

		assert.True(t, "find echo", checkStdout(t, process, expected, []string{}))

		// Make a simultaneously active connection.
		ws2, server2 := mockConn(ctx, t, &Options{
			// Divide the time to test that the heartbeat keeps it open through multiple
			// intervals.
			SessionTimeout: time.Second / 4,
		})
		defer server2.Close()

		process, err = RemoteExecer(ws2).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// Disconnect the first connection.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		// Wait for inactivity.  It should still stay up because of the second
		// connection.
		time.Sleep(time.Second)

		// This connection should still be up.
		echoCmd = "echo test:$((3+3))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		expected = append(expected, echoCmd, "test:6")

		assert.True(t, "find echo", checkStdout(t, process, expected, []string{}))

		// Disconnect the second connection.
		ws2.Close(websocket.StatusNormalClosure, "disconnected")
		server2.Close()

		// Wait for inactivity.
		time.Sleep(time.Second)

		// The next connection should start a new process.
		ws, server = mockConn(ctx, t, &Options{
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		echoCmd = "echo test:$((4+4))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)
		unexpected := expected
		expected = []string{"test:8"}

		// This time no echo since it is a new process.
		assert.True(t, "find echo", checkStdout(t, process, expected, unexpected))
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
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err := RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// Run an application that enters the alternate screen.
		_, err = process.Stdin().Write([]byte("./ci/alt.sh\r\n"))
		assert.Success(t, "write to stdin", err)

		assert.True(t, "find output", checkStdout(t, process, []string{"./ci/alt.sh", "ALT SCREEN"}, []string{}))

		// Disconnect.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		// Reconnect; the application should redraw.
		ws, server = mockConn(ctx, t, &Options{
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		// Should have only the application output.
		assert.True(t, "find output", checkStdout(t, process, []string{"ALT SCREEN"}, []string{"./ci/alt.sh"}))

		// Exit the application.
		_, err = process.Stdin().Write([]byte("q"))
		assert.Success(t, "write to stdin", err)

		// Disconnect.
		ws.Close(websocket.StatusNormalClosure, "disconnected")
		server.Close()

		// Reconnect.
		ws, server = mockConn(ctx, t, &Options{
			SessionTimeout: time.Second,
		})
		defer server.Close()

		process, err = RemoteExecer(ws).Start(ctx, command)
		assert.Success(t, "attach sh", err)

		echoCmd := "echo test:$((5+5))"
		_, err = process.Stdin().Write([]byte(echoCmd + "\r\n"))
		assert.Success(t, "write to stdin", err)

		assert.True(t, "find output", checkStdout(t, process, []string{echoCmd, "test:10"}, []string{}))
	})
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
				return false
			}
		}
		if strings.Contains(line, expected[i]) {
			i = i + 1
		}
		if i == len(expected) {
			return true
		}
	}
	return false
}
