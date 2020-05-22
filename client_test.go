package wsep

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cdr.dev/slog/sloggers/slogtest/assert"
	"cdr.dev/wsep/internal/proto"
	"github.com/google/go-cmp/cmp"
	"go.coder.com/flog"
	"nhooyr.io/websocket"
)

func TestRemoteStdin(t *testing.T) {
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

		assert.Equal(t, "stdin body", body, []byte(tcase), bytecmp)
		assert.Equal(t, "stdin header", header, []byte(`{"type":"stdin"}`), bytecmp)
	}
}

func TestExec(t *testing.T) {
	mockServerHandler := func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		err = Serve(r.Context(), ws, LocalExecer{})
		if err != nil {
			flog.Error("failed to serve execer: %v", err)
			ws.Close(websocket.StatusAbnormalClosure, "failed to serve execer")
			return
		}
		ws.Close(websocket.StatusNormalClosure, "normal closure")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(mockServerHandler))
	defer server.Close()

	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	assert.Success(t, "dial websocket server", err)
	defer ws.Close(websocket.StatusAbnormalClosure, "abnormal closure")

	execer := RemoteExecer(ws)
	process, err := execer.Start(ctx, Command{
		Command: "pwd",
	})
	assert.Success(t, "start process", err)

	assert.Equal(t, "pid", false, process.Pid() == 0)

	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		defer wg.Done()

		stdout, err := ioutil.ReadAll(process.Stdout())
		assert.Success(t, "read stdout", err)
		wd, err := os.Getwd()
		assert.Success(t, "get real working dir", err)

		assert.Equal(t, "stdout", wd, strings.TrimSuffix(string(stdout), "\n"))
	}()
	go func() {
		wg.Add(1)
		defer wg.Done()
		stderr, err := ioutil.ReadAll(process.Stderr())
		assert.Success(t, "read stderr", err)
		assert.Equal(t, "len stderr", 0, len(stderr))
	}()

	err = process.Wait()
	assert.Success(t, "wait", err)

	wg.Wait()
}
