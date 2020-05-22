package wsep

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cdr.dev/slog/sloggers/slogtest/assert"
)

func TestLocalExec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	execer := LocalExecer{}
	process, err := execer.Start(ctx, Command{
		Command: "pwd",
	})
	assert.Success(t, "start local cmd", err)

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
	assert.Success(t, "wait for process to complete", err)

	wg.Wait()
}