package proto

// Client message header type
const (
	TypeStart      = "start"
	TypeResize     = "resize"
	TypeStdin      = "stdin"
	TypeCloseStdin = "close_stdin"
)

// ClientResizeHeader specifies a terminal window resize request
type ClientResizeHeader struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// ClientStartHeader specifies a request to start command
type ClientStartHeader struct {
	Type    string  `json:"type"`
	ID      string  `json:"id"`
	Command Command `json:"command"`
}

// Command represents a runnable command.
type Command struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Stdin      bool     `json:"stdin"`
	TTY        bool     `json:"tty"`
	Rows       uint16   `json:"rows"`
	Cols       uint16   `json:"cols"`
	UID        uint32   `json:"uid"`
	GID        uint32   `json:"gid"`
	Env        []string `json:"env"`
	WorkingDir string   `json:"working_dir"`
}
