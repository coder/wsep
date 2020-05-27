package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"cdr.dev/wsep"
	"github.com/spf13/pflag"
	"go.coder.com/cli"
	"go.coder.com/flog"
	"golang.org/x/crypto/ssh/terminal"
	"nhooyr.io/websocket"
)

type notty struct {
}

func (c *notty) Run(fl *pflag.FlagSet) {
	do(fl, false)
}

func (c *notty) Spec() cli.CommandSpec {
	return cli.CommandSpec{
		Name:    "notty",
		Usage:   "[flags]",
		Desc:    `Run a command without tty enabled.`,
		RawArgs: true,
	}
}

type tty struct {
}

func (c *tty) Run(fl *pflag.FlagSet) {
	do(fl, true)
}

func (c *tty) Spec() cli.CommandSpec {
	return cli.CommandSpec{
		Name:    "tty",
		Usage:   "[flags]",
		Desc:    `Run a command with tty enabled.`,
		RawArgs: true,
	}
}

func do(fl *pflag.FlagSet, tty bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws://localhost:8080", nil)
	if err != nil {
		flog.Fatal("failed to dial remote executor: %v", err)
	}
	defer conn.Close(websocket.StatusAbnormalClosure, "terminate process")

	executor := wsep.RemoteExecer(conn)

	var args []string
	if len(fl.Args()) < 1 {
		flog.Fatal("a command argument is required")
	}
	if len(fl.Args()) > 1 {
		args = fl.Args()[1:]
	}
	process, err := executor.Start(ctx, wsep.Command{
		Command: fl.Arg(0),
		Args:    args,
		TTY:     tty,
	})
	if err != nil {
		flog.Fatal("failed to start remote command: %v", err)
	}
	if tty {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		go func() {
			for range ch {
				width, height, err := terminal.GetSize(int(os.Stdin.Fd()))
				if err != nil {
					continue
				}
				process.Resize(ctx, uint16(height), uint16(width))
			}
		}()
		ch <- syscall.SIGWINCH

		oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			flog.Fatal("failed to make terminal raw for tty: %w", err)
		}
		defer terminal.Restore(int(os.Stdin.Fd()), oldState)
	}

	go io.Copy(os.Stdout, process.Stdout())
	go io.Copy(os.Stderr, process.Stderr())
	go func() {
		stdin := process.Stdin()
		defer stdin.Close()
		io.Copy(stdin, os.Stdin)
	}()

	err = process.Wait()
	if err != nil {
		flog.Error("process failed: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "normal closure")
}

type cmd struct {
}

func (c *cmd) Spec() cli.CommandSpec {
	return cli.CommandSpec{
		Name:    "wsep-client",
		Usage:   "[flags]",
		Desc:    `Run a simple wsep client for testing.`,
		RawArgs: true,
	}
}

func (c *cmd) Run(fl *pflag.FlagSet) {
	fl.Usage()
	os.Exit(1)
}
func (c *cmd) Subcommands() []cli.Command {
	return []cli.Command{
		&notty{},
		&tty{},
	}
}

func main() {
	cli.RunRoot(&cmd{})
}
