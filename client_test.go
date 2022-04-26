package wsep

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cdr.dev/slog/sloggers/slogtest/assert"
	"cdr.dev/wsep/internal/proto"
	"github.com/google/go-cmp/cmp"
	"nhooyr.io/websocket"
)

func TestRemoteStdin(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"pwd",
		"echo 123\n456",
		"\necho 123456\n",
	}

	for _, tcase := range inputs {
		server, client := net.Pipe()
		var stdin io.WriteCloser = remoteStdin{
			conn: client,
		}
		go func() {
			defer client.Close()
			_, err := stdin.Write([]byte(tcase))
			assert.Success(t, "write to stdin", err)
		}()

		bytecmp := cmp.Comparer(bytes.Equal)

		msg, err := ioutil.ReadAll(server)
		assert.Success(t, "read from server", err)

		header, body := proto.SplitMessage(msg)

		assert.Equal(t, "stdin body", []byte(tcase), body, bytecmp)
		assert.Equal(t, "stdin header", []byte(`{"type":"stdin"}`), header, bytecmp)
	}
}

func mockConn(ctx context.Context, t *testing.T) (*websocket.Conn, *httptest.Server) {
	mockServerHandler := func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		err = Serve(r.Context(), ws, LocalExecer{}, nil)
		if err != nil {
			t.Errorf("failed to serve execer: %v", err)
			ws.Close(websocket.StatusAbnormalClosure, "failed to serve execer")
			return
		}
		ws.Close(websocket.StatusNormalClosure, "normal closure")
	}

	server := httptest.NewServer(http.HandlerFunc(mockServerHandler))

	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	assert.Success(t, "dial websocket server", err)
	return ws, server
}

func TestRemoteExec(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ws, server := mockConn(ctx, t)
	defer server.Close()

	execer := RemoteExecer(ws)
	testExecer(ctx, t, execer)
}

func TestRemoteExecFail(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ws, server := mockConn(ctx, t)
	defer server.Close()

	execer := RemoteExecer(ws)
	testExecerFail(ctx, t, execer)
}

func testExecerFail(ctx context.Context, t *testing.T, execer Execer) {
	process, err := execer.Start(ctx, Command{
		Command: "ls",
		Args:    []string{"/doesnotexist"},
	})
	assert.Success(t, "start local cmd", err)

	go io.Copy(ioutil.Discard, process.Stderr())
	go io.Copy(ioutil.Discard, process.Stdout())

	err = process.Wait()
	code, ok := err.(ExitError)
	assert.True(t, "is exit error", ok)
	assert.True(t, "exit code is nonzero", code.Code != 0)
	assert.Error(t, "wait for process to error", err)
}

func TestStderrVsStdout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	ws, server := mockConn(ctx, t)
	defer server.Close()

	execer := RemoteExecer(ws)
	process, err := execer.Start(ctx, Command{
		Command: "sh",
		Args:    []string{"-c", "echo stdout-message; echo 1>&2 stderr-message"},
		Stdin:   false,
	})
	assert.Success(t, "start command", err)

	go io.Copy(&stdout, process.Stdout())
	go io.Copy(&stderr, process.Stderr())

	err = process.Wait()
	assert.Success(t, "wait for process to complete", err)

	assert.Equal(t, "stdout", "stdout-message", strings.TrimSpace(stdout.String()))
	assert.Equal(t, "stderr", "stderr-message", strings.TrimSpace(stderr.String()))
}
