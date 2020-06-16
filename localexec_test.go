package wsep

import (
	"context"
	"io"
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

	testExecer(ctx, t, LocalExecer{})
}

func testExecer(ctx context.Context, t *testing.T, execer Execer) {
	process, err := execer.Start(ctx, Command{
		Command: "pwd",
	})
	assert.Success(t, "start local cmd", err)
	var (
		stderr = process.Stderr()
		stdout = process.Stdout()
		wg     sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()

		stdoutByt, err := ioutil.ReadAll(stdout)
		assert.Success(t, "read stdout", err)
		wd, err := os.Getwd()
		assert.Success(t, "get real working dir", err)

		assert.Equal(t, "stdout", wd, strings.TrimSuffix(string(stdoutByt), "\n"))
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()

		stderrByt, err := ioutil.ReadAll(stderr)
		assert.Success(t, "read stderr", err)
		assert.True(t, "len stderr", len(stderrByt) == 0)
	}()

	wg.Wait()
	err = process.Wait()
	assert.Success(t, "wait for process to complete", err)
}

func TestExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	process, err := LocalExecer{}.Start(ctx, Command{
		Command: "/bin/sh",
		Args:    []string{"-c", `"fakecommand"`},
	})
	assert.Success(t, "start local cmd", err)

	err = process.Wait()
	exitErr, ok := err.(ExitError)
	assert.True(t, "error is ExitError", ok)
	assert.Equal(t, "exit error", exitErr.Code, 127)
}

func TestStdin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var execer LocalExecer
	process, err := execer.Start(ctx, Command{
		Command: "cat",
		Stdin:   true,
	})
	assert.Success(t, "start command", err)

	go func() {
		stdin := process.Stdin()
		defer stdin.Close()
		_, err := io.Copy(stdin, strings.NewReader("testing value"))
		assert.Success(t, "copy stdin", err)
	}()

	io.Copy(os.Stdout, process.Stdout())
	err = process.Wait()
	assert.Success(t, "process wait", err)
}

func TestStdinFail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var execer LocalExecer
	process, err := execer.Start(ctx, Command{
		Command: "cat",
		Stdin:   false,
	})
	assert.Success(t, "start command", err)

	go func() {
		stdin := process.Stdin()
		defer stdin.Close()
		_, err := io.Copy(stdin, strings.NewReader("testing value"))
		assert.Error(t, "copy stdin should fail", err)
	}()

	io.Copy(os.Stdout, process.Stdout())
	err = process.Wait()
	assert.Success(t, "process wait", err)
}

func TestCombinedOutput(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		out, err := CombinedOutput(context.Background(), LocalExecer{}, Command{
			Command: "echo",
			Args:    []string{"hello"},
		})
		assert.Success(t, "failed to run command", err)
		assert.Equal(t, "unexpected output", "hello\n", string(out))
	})

	t.Run("StdErr", func(t *testing.T) {
		out, err := CombinedOutput(context.Background(), LocalExecer{}, Command{
			Command: "/bin/bash",
			Args:    []string{"-c", ">&2 echo 'error' && exit 1"},
		})
		assert.Error(t, "command unexpectedly succeeded", err)
		assert.Equal(t, "unexpected output", "error\n", string(out))
	})
}
