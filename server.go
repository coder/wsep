package wsep

import "nhooyr.io/websocket"

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(c *websocket.Conn, execer Execer) error {

}