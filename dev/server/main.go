package main

import (
	"net/http"

	"cdr.dev/wsep"
	"go.coder.com/flog"
	"nhooyr.io/websocket"
)

func main() {
	server := http.Server{
		Addr:    ":8080",
		Handler: server{},
	}
	err := server.ListenAndServe()
	flog.Fatal("failed to listen: %v", err)
}

type server struct{}

func (s server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	err = wsep.Serve(r.Context(), ws, wsep.LocalExecer{})
	if err != nil {
		flog.Fatal("failed to serve local execer: %v", err)
	}
}
