module cdr.dev/wsep

go 1.14

require (
	cdr.dev/slog v1.3.0
	github.com/armon/circbuf v0.0.0-20190214190532-5111143e8da2
	github.com/creack/pty v1.1.12
	github.com/google/go-cmp v0.5.5
	github.com/google/uuid v1.3.0
	github.com/liamg/darktile v0.0.11 // indirect
	github.com/spf13/pflag v1.0.5
	go.coder.com/cli v0.4.0
	go.coder.com/flog v0.0.0-20190906214207-47dd47ea0512
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/term v0.0.0-20201126162022-7de9c90e9dd1
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1
	nhooyr.io/websocket v1.8.6
)

replace github.com/liamg/darktile => /home/coder/src/darktile
