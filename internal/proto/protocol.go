package proto

import (
	"bytes"
	"io"
)

// Header is a generic JSON header.
type Header struct {
	Type string `json:"type"`
}

// delimiter splits the message header from the body
const delimiter = '\n'

// SplitMessage into header and body components
// all messages must have a header. body returns as nil if no delimiter is found
func SplitMessage(b []byte) (header []byte, body []byte) {
	ix := bytes.IndexRune(b, delimiter)
	if ix == -1 {
		return b, nil
	}
	header = b[:ix]
	if ix < len(b)-1 {
		body = b[ix+1:]
	}
	return header, body
}

type headerWriter struct {
	w      io.WriteCloser
	header []byte
}

// WithHeader adds the given header to all writes
func WithHeader(w io.WriteCloser, header []byte) io.WriteCloser {
	return headerWriter{
		header: header,
		w:      w,
	}
}

func (h headerWriter) Write(b []byte) (int, error) {
	msg := append(append(h.header, delimiter), b...)
	_, err := h.w.Write(msg)
	if err != nil {
		return 0, err
	}
	return len(b), nil // TODO: potential buggy
}

func (h headerWriter) Close() error {
	return h.w.Close()
}
