package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"cdr.dev/wsep"
	"go.coder.com/flog"
	"nhooyr.io/websocket"
)

const (
	upgradeHeader    = "Upgrade"
	upgradeWebsocket = "websocket"
)

func IsUpgradeRequest(r *http.Request) bool {
	vs := r.Header.Values(upgradeHeader)
	for _, v := range vs {
		if v == upgradeWebsocket {
			return true
		}
	}
	return false
}

func main() {
	server := http.Server{
		Addr:    ":8080",
		Handler: http.HandlerFunc(serve),
	}
	err := server.ListenAndServe()
	flog.Fatal("failed to listen: %v", err)
}

func serve(w http.ResponseWriter, r *http.Request) {
	if IsUpgradeRequest(r) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		err = wsep.Serve(r.Context(), ws, wsep.LocalExecer{}, &wsep.Options{
			SessionTimeout: 30 * time.Second,
		})
		if err != nil {
			flog.Error("failed to serve execer: %v", err)
			ws.Close(websocket.StatusAbnormalClosure, "failed to serve execer")
			return
		}
		ws.Close(websocket.StatusNormalClosure, "normal closure")
		return
	}

	fmt.Println("path:", r.URL.Path)
	p := r.URL.Path
	if r.URL.Path == "/" {
		p = "./browser/index.html"
	}

	dir, err := os.Getwd()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	file := filepath.Join(dir, p)

	fmt.Println("serving: ", file)
	b, err := os.ReadFile(file)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Write(b)
}
