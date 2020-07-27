package wsep

import (
	"context"
	"io/ioutil"
	"strings"
	"sync"
	"testing"
	"time"

	"cdr.dev/slog/sloggers/slogtest/assert"
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
