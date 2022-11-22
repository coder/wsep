package proto

// Server message header type
const (
	TypePid      = "pid"
	TypeStdout   = "stdout"
	TypeStderr   = "stderr"
	TypeExitCode = "exit_code"
)

// ServerPidHeader specifies the message send immediately after the request command starts
type ServerPidHeader struct {
	Type string `json:"type"`
	Pid  int    `json:"pid"`
}

// ServerExitCodeHeader specifies the final message from the server after the command exits
type ServerExitCodeHeader struct {
	Type     string `json:"type"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
}
