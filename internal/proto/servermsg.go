package proto

type ServerPidHeader struct {
	Pid int `json:"pid"`
}

type ServerExitCodeHeader struct {
	ExitCode int `json:"exit_code"`
}
