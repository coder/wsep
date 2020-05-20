package proto

const (
	TypePid      = "pid"
	TypeStdout   = "stdout"
	TypeStderr   = "stderr"
	TypeExitCode = "exit_code"
)

type ServerPidHeader struct {
	Pid int `json:"pid"`
}

type ServerExitCodeHeader struct {
	ExitCode int `json:"exit_code"`
}
