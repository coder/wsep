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
		Handler: http.HandlerFunc(serve),
	}
	err := server.ListenAndServe()
	flog.Fatal("failed to listen: %v", err)
}

func serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	err = wsep.Serve(r.Context(), ws, wsep.LocalExecer{})
	if err != nil {
		flog.Error("failed to serve execer: %v", err)
		ws.Close(websocket.StatusAbnormalClosure, "failed to serve execer")
		return
	}
	ws.Close(websocket.StatusNormalClosure, "normal closure")
}
