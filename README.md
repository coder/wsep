# wsep

`wsep` is a high performance command execution protocol over WebSocket. It can be thought of as SSH over WebSockets without encryption.

The package offers the `wsep.Execer` interface so that local, SSH, and WebSocket execution can be interchanged. This is particular useful when testing.

## Examples

Error handling is omitted for brevity.

### Client

```golang
conn, _, _ := websocket.Dial(ctx, "ws://remote.exec.addr", nil)
defer conn.Close(websocket.StatusAbnormalClosure, "terminate process")

execer := wsep.RemoteExecer(conn)
process, _ := execer.Start(ctx, wsep.Command{
  Command: "cat",
  Args:    []string{"go.mod"},
  Stdin:   false,
})

go io.Copy(os.Stderr, process.Stderr())
go io.Copy(os.Stdout, process.Stdout())

process.Wait()
conn.Close(websocket.StatusNormalClosure, "normal closure")
```

### Server

```golang
func (s server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  conn, _ := websocket.Accept(w, r, nil)

  wsep.Serve(r.Context(), conn, wsep.LocalExecer{})

  ws.Close(websocket.StatusNormalClosure, "normal closure")
}
```

### Development / Testing

Start a local executor:

```sh
go run ./dev/server
```

Start a client:

```sh
go run ./dev/client tty --id 1 -- bash
go run ./dev/client notty -- ls -la
```

### Benchmarks

Local `sh` through a local `wsep` connection

```shell script
$ head -c 100000000 /dev/urandom > /tmp/random; cat /tmp/random | pv | time ./bin/client notty -- sh -c "cat > /dev/null"

95.4MiB 0:00:00 [ 269MiB/s] [ <=>                                                                                  ]
./bin/client notty -- sh -c "cat > /dev/null"  0.32s user 0.31s system 31% cpu 2.019 total
```

Local `sh` directly

```shell script
$ head -c 100000000 /dev/urandom > /tmp/random; cat /tmp/random | pv | time  sh -c "cat > /dev/null"

95.4MiB 0:00:00 [1.73GiB/s] [ <=>                                                                                  ]
sh -c "cat > /dev/null"  0.00s user 0.02s system 32% cpu 0.057 total
```
