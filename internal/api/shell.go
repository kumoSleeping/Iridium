package api

// ShellRun asks to run one command line at a session's machine, in a folder, without its agent
// (docs/api.md "命令"). "cd <dir>" runs nothing: it answers the folder it moves to.
type ShellRun struct {
	Line string `json:"line"`
	Cwd  string `json:"cwd,omitempty"` // "": the session's folder
}

// ShellStarted answers a ShellRun: the run to read, "" for a cd; Cwd is the folder it runs in.
type ShellStarted struct {
	Run string `json:"run,omitempty"`
	Cwd string `json:"cwd"`
}

// ShellOutput is a run's output from an offset: its stdout and stderr together, as they came.
type ShellOutput struct {
	Data   string `json:"data"`
	Next   int64  `json:"next"` // the offset to read from next
	Done   bool   `json:"done"` // it ended and all it wrote is in
	Code   *int   `json:"code,omitempty"`
	Signal string `json:"signal,omitempty"`
	Err    string `json:"err,omitempty"` // once done: what could not run it, if anything
}
