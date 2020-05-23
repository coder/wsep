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
		Command: "bash",
		TTY:     true,
	})
	assert.Success(t, "start bash", err)
	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		defer wg.Done()

		stdout, err := ioutil.ReadAll(process.Stdout())
		assert.Success(t, "read stdout", err)
		t.Logf("bash tty stdout = %s", stdout)
		assert.True(t, `bash "$" prompt found`, strings.HasSuffix(string(stdout), "$ "))
	}()
	go func() {
		wg.Add(1)
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
