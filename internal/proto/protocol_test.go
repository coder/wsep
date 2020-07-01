package proto

import (
	"bytes"
	"io/ioutil"
	"testing"

	"cdr.dev/slog/sloggers/slogtest/assert"
	"github.com/google/go-cmp/cmp"
)

func TestWithHeader(t *testing.T) {
	tests := []struct {
		inputheader, inputbody, header, body []byte
	}{
		{
			inputheader: []byte("header"),
			inputbody:   []byte("body"),
			header:      []byte("header"),
			body:        []byte("body"),
		},
		{
			inputheader: []byte("header"),
			inputbody:   []byte("b\nody\n"),
			header:      []byte("header"),
			body:        []byte("b\nody\n"),
		},
	}
	bytecmp := cmp.Comparer(bytes.Equal)

	for _, tcase := range tests {
		b := bytes.NewBuffer(nil)
		withheader := WithHeader(b, []byte(tcase.inputheader))
		_, err := withheader.Write([]byte(tcase.inputbody))
		assert.Success(t, "write to buffer with header", err)

		msg, err := ioutil.ReadAll(b)
		assert.Success(t, "read buffer", err)

		header, body := SplitMessage(msg)
		assert.Equal(t, "header is expected value", tcase.header, header, bytecmp)
		assert.Equal(t, "body is expected value", tcase.body, body, bytecmp)
	}
}
