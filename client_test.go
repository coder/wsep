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
		err = Serve(r.Context(), ws, LocalExecer{})
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ws, server := mockConn(ctx, t)
	defer ws.Close(websocket.StatusAbnormalClosure, "abnormal closure")
	defer server.Close()

	execer := RemoteExecer(ws)
	testExecer(ctx, t, execer)
}
