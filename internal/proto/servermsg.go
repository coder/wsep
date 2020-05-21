package proto

const (
	TypePid      = "pid"
	TypeStdout   = "stdout"
	TypeStderr   = "stderr"
	TypeExitCode = "exit_code"
)

type ServerPidHeader struct {
	Type string `json:"type"`
	Pid  int    `json:"pid"`
}

type ServerExitCodeHeader struct {
	Type     string `json:"type"`
	ExitCode int    `json:"exit_code"`
}
