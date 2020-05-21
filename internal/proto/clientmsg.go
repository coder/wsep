package proto

const (
	TypeStart      = "start"
	TypeResize     = "resize_header"
	TypeStdin      = "stdin"
	TypeCloseStdin = "close_stdin"
)

type ClientResizeHeader struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type ClientStartHeader struct {
	Type    string  `json:"type"`
	Command Command `json:"command"`
}

// Command represents a runnable command.
type Command struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	TTY        bool     `json:"tty"`
	UID        uint32   `json:"uid"`
	GID        uint32   `json:"gid"`
	Env        []string `json:"env"`
	WorkingDir string   `json:"working_dir"`
}
