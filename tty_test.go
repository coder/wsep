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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws1, server1 := mockConn(ctx, t, &Options{
		ReconnectingProcessTimeout: time.Second,
	})
	defer server1.Close()

	command := Command{
		ID:      uuid.NewString(),
		Command: "sh",
		TTY:     true,
		Stdin:   true,
	}
	execer1 := RemoteExecer(ws1)
	process1, err := execer1.Start(ctx, command)
	assert.Success(t, "start sh", err)

	// Write some unique output.
	echoCmd := "echo test:$((1+1))"
	data := []byte(echoCmd + "\r\n")
	_, err = process1.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)
	expected := []string{echoCmd, "test:2"}

	assert.True(t, "find echo", findEcho(t, process1, expected))

	// Test disconnecting then reconnecting.
	process1.Close()
	server1.Close()

	ws2, server2 := mockConn(ctx, t, &Options{
		ReconnectingProcessTimeout: time.Second,
	})
	defer server2.Close()

	execer2 := RemoteExecer(ws2)
	process2, err := execer2.Start(ctx, command)
	assert.Success(t, "attach sh", err)

	// The inactivity timeout should not have been triggered.
	time.Sleep(time.Second)

	echoCmd = "echo test:$((2+2))"
	data = []byte(echoCmd + "\r\n")
	_, err = process2.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)
	expected = append(expected, echoCmd, "test:4")

	assert.True(t, "find echo", findEcho(t, process2, expected))

	// Test disconnecting while another connection is active.
	ws3, server3 := mockConn(ctx, t, &Options{
		// Divide the time to test that the heartbeat keeps it open through multiple
		// intervals.
		ReconnectingProcessTimeout: time.Second / 4,
	})
	defer server3.Close()

	execer3 := RemoteExecer(ws3)
	process3, err := execer3.Start(ctx, command)
	assert.Success(t, "attach sh", err)

	process2.Close()
	server2.Close()
	time.Sleep(time.Second)

	// This connection should still be up.
	echoCmd = "echo test:$((3+3))"
	data = []byte(echoCmd + "\r\n")
	_, err = process3.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)
	expected = append(expected, echoCmd, "test:6")

	assert.True(t, "find echo", findEcho(t, process3, expected))

	// Close the remaining connection and wait for inactivity.
	process3.Close()
	server3.Close()
	time.Sleep(time.Second)

	// The next connection should start a new process.
	ws4, server4 := mockConn(ctx, t, &Options{
		ReconnectingProcessTimeout: time.Second,
	})
	defer server4.Close()

	execer4 := RemoteExecer(ws4)
	process4, err := execer4.Start(ctx, command)
	assert.Success(t, "attach sh", err)

	// This time no echo since it is a new process.
	notExpected := expected
	echoCmd = "echo done"
	data = []byte(echoCmd + "\r\n")
	_, err = process4.Stdin().Write(data)
	assert.Success(t, "write to stdin", err)
	expected = []string{echoCmd, "done"}
	assert.True(t, "find echo", findEcho(t, process4, expected, notExpected...))
	assert.Success(t, "context", ctx.Err())
}


func findEcho(t *testing.T, process Process, expected []string, notExpected ...string) bool {
	scanner := bufio.NewScanner(process.Stdout())
outer:
	for _, str := range expected {
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("bash tty stdout = %s", line)
			for _, bad := range notExpected {
				if strings.Contains(line, bad) {
					return false
				}
			}
			if strings.Contains(line, str) {
				continue outer
			}
		}
		return false // Reached the end of output without finding str.
	}
	return true
}