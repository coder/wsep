# wsep

`wsep` is a high performance, ***W***eb***S***ocket command ***e***xecution ***p***rotocol. It can be thought of as SSH without encryption.

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

go io.Copy(os.Stdout, process.Stdout())
go io.Copy(os.Stderr, process.Stderr())
go io.Copy(process.Stdin(), os.Stdin)

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
