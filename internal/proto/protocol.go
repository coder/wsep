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
func SplitMessage(b []byte) (header []byte, body []byte) {
	parts := bytes.SplitAfterN(b, []byte{delimiter}, 1)
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts[0], parts[1]
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
	return h.w.Write(msg)
}

func (h headerWriter) Close() error {
	return h.w.Close()
}
