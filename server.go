package wsep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"

	"golang.org/x/xerrors"
	"nhooyr.io/websocket"

	"cdr.dev/wsep/internal/proto"
)

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer) error {
	var (
		header  proto.Header
		process Process
		copyBuf = make([]byte, 32<<10)
	)
	defer func() {
		if process != nil {
			process.Close()
		}
	}()
	for {
		typ, reader, err := c.Reader(ctx)
		if err != nil {
			return xerrors.Errorf("get reader: %w", err)
		}
		if typ != websocket.MessageText {
			return xerrors.Errorf("expected text message first: %w", err)
		}
		// Allocate, because we have to read header twice.
		byt, err := ioutil.ReadAll(reader)
		if err != nil {
			return xerrors.Errorf("read header: %w", err)
		}

		err = json.Unmarshal(byt, &header)
		if err != nil {
			return xerrors.Errorf("unmarshal header: %w", err)
		}

		switch header.Type {
		case proto.TypeStart:
			header := proto.ClientStartHeader{}
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal start header: %w", err)
			}
			process, err = execer.Start(ctx, Command{
				Command: header.Command,
				Args:    header.Args,
				TTY:     header.TTY,
			})
			if err != nil {
				return err
			}
			go func() {
				process.Stdout()
			}()
		case proto.TypeResize:
			if process == nil {
				return errors.New("resize sent before command started")
			}

			header := proto.ClientResizeHeader{}
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal resize header: %w", err)
			}

			err = process.Resize(header.Rows, header.Cols)
			if err != nil {
				return xerrors.Errorf("resize: %w", err)
			}
		case proto.TypeStdin:
			_, reader, err := c.Reader(ctx)
			if err != nil {
				return xerrors.Errorf("get stdin reader: %w", err)
			}

			_, err = io.CopyBuffer(process.Stdin(), reader, copyBuf)
			if err != nil {
				return xerrors.Errorf("read stdin: %w", err)
			}
		case proto.TypeCloseStdin:
			wr, err := c.Writer(ctx, websocket.MessageText)
			if err != nil {
				return xerrors.Errorf("get writer: %w", err)
			}
			err = json.NewEncoder(wr).Encode(&proto.Header{Type: "close_stdin"})
			if err != nil {
				return xerrors.Errorf("encode close_stdin: %w", err)
			}
		}
	}
}
