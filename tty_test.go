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

	ws, server := mockConn(ctx, t)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws, server := mockConn(ctx, t)
	defer server.Close()

	command := Command{
		ID:      uuid.NewString(),
		Command: "sh",
		TTY:     true,
		Stdin:   true,
	}
	execer := RemoteExecer(ws)
	process, err := execer.Start(ctx, command)
	assert.Success(t, "start sh", err)

	// Write some unique output.
	echoCmd := "echo test:$((1+1))"
	data := []byte(echoCmd + "\r\n")
	_, err = process.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)

	var scanner *bufio.Scanner
	findEcho := func(expected string) bool {
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("bash tty stdout = %s", line)
			if strings.Contains(line, expected) {
				return true
			}
		}
		return false
	}

	// Once for typing the command...
	scanner = bufio.NewScanner(process.Stdout())
	assert.True(t, "find command", findEcho(echoCmd))
	// And another time for the actual output.
	assert.True(t, "find command eval", findEcho("test:2"))

	// Disconnect.
	ws.Close(websocket.StatusNormalClosure, "disconnected")
	server.Close()

	ws, server = mockConn(ctx, t)
	defer server.Close()

	execer = RemoteExecer(ws)
	process, err = execer.Start(ctx, command)
	assert.Success(t, "attach sh", err)

	// Same output again.
	scanner = bufio.NewScanner(process.Stdout())
	assert.True(t, "find command", findEcho(echoCmd))
	assert.True(t, "find command eval", findEcho("test:2"))

	// Should be able to use the new connection.
	echoCmd = "echo test:$((2+2))"
	data = []byte(echoCmd + "\r\n")
	_, err = process.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)

	assert.True(t, "find command", findEcho(echoCmd))
	assert.True(t, "find command eval", findEcho("test:4"))
}
