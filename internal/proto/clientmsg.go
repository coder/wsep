package proto

type ClientStartHeader struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	TTY     bool     `json:"tty"`
}

type ClientResizeHeader struct {
	Height int `json:"height"`
	Width  int `json:"width"`
}
