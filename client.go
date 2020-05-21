package wsep

import "nhooyr.io/websocket"

// RemoteExecer creates an execution interface from a WebSocket connection.
func RemoteExecer(conn *websocket.Conn) Execer {
	return nil
}
