package wsep

import (
	"bytes"
	"io"
	"io/ioutil"
	"net"
	"testing"

	"cdr.dev/slog/sloggers/slogtest/assert"
	"cdr.dev/wsep/internal/proto"
	"github.com/google/go-cmp/cmp"
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
