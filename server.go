package wsep

import (
	"context"
	"io/ioutil"

	"cdr.dev/wsep/internal/proto"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"
)

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer) error {
	var header proto.Header
	for {
		typ, reader, err := c.Reader(ctx)
		if err != nil {
			return xerrors.Errorf("get reader: %w", err)
		}
		switch typ {
		case websocket.MessageText:
			// Header

			// Allocate, because we have to read it twice.
			byt, err := ioutil.ReadAll(reader)
			if err != nil {
				return xerrors.Errorf("read header: %w", err)
			}
		case websocket.MessageBinary:
			// Body
		}
	}
}