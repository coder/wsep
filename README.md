# wsep

`wsep` is a high performance,
<strong style="font-size: 1.5em; text-decoration: underline;">w</strong>eb <strong style="font-size: 1.5em;text-decoration: underline;">s</strong>ocket command <strong style="font-size: 1.5em;text-decoration: underline;">e</strong>xecution <strong style="font-size: 1.5em;text-decoration: underline;">p</strong>rotocol. It can be thought of as SSH without encryption.

It's useful in cases where you want to provide a command exec interface into a remote environment. It's implemented
with WebSocket so it may be used directly by a browser frontend.

## Examples

### Client

```golang
ws, _, err := websocket.Dial(ctx, "ws://remote.exec.addr", nil)
if err != nil {
  // handle error
}
defer conn.Close(websocket.StatusAbnormalClosure, "terminate process")

executor := wsep.RemoteExecer(ws)
process, err := executor.Start(ctx, proto.Command{
  Command: "cat",
  Args: []string{"go.mod"},
})
if err != nil {
  // handle error
}

io.Copy(os.Stderr, process.Stderr())

err = process.Wait()
if err != nil {
  // process failed
} else {
  // process succeeded
}
conn.Close(websocket.StatusNormalClosure, "normal closure")
```

### Server

```golang
func (s server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	err = wsep.Serve(r.Context(), ws, wsep.LocalExecer{})
	if err != nil {
		ws.Close(websocket.StatusAbnormalClosure, "failed to serve execer")
		return
	}
	ws.Close(websocket.StatusNormalClosure, "normal closure")
}
```

### Development / Testing

Start a local executor:

```
go run ./dev/server
```

Start a client:

```
go run ./dev/client tty bash
go run ./dev/client notty ls
```

### Local performance cost

Local `sh` through a local `wsep` connection

```shellscript
$ head -c 100000000 /dev/urandom > /tmp/random; cat /tmp/random | pv | time ./bin/client notty sh -c "cat > /dev/null"

95.4MiB 0:00:00 [ 269MiB/s] [ <=>                                                                                  ]
./bin/client notty sh -c "cat > /dev/null"  0.32s user 0.31s system 31% cpu 2.019 total
```

Local `sh` directly

```shell script
$ head -c 100000000 /dev/urandom > /tmp/random; cat /tmp/random | pv | time  sh -c "cat > /dev/null"

95.4MiB 0:00:00 [1.73GiB/s] [ <=>                                                                                  ]
sh -c "cat > /dev/null"  0.00s user 0.02s system 32% cpu 0.057 total
```

### Performance Goals

Test command

```shell script
head -c 100000000 /dev/urandom > /tmp/random; cat /tmp/random | pv | time coder sh ammar sh -c "cat > /dev/null"
```

streams at

```shell script
95.4MiB 0:00:08 [10.7MiB/s] [                                <=>                                                                                                                                                  ]
       15.34 real         2.16 user         1.54 sys
```

The same command over wush stdout (which is high performance) streams at 17MB/s.
