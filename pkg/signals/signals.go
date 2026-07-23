// Package signals interprets process exit codes, focusing on the 128+N
// convention where a non-zero code means the process was terminated by signal N.
//
// On Semaphore this matters because a tool exiting 130 (SIGINT) leads the agent
// to mark the job STOPPED rather than FAILED — which silently suppresses the
// usual "build failed" notifications. Surfacing the signal turns an opaque
// exit code into an actionable explanation.
package signals

import "fmt"

// Info describes an exit code's signal interpretation. It is purely additive —
// callers keep the raw exit code and attach this alongside it.
type Info struct {
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`    // e.g. "SIGINT"
	SignalNo int    `json:"signal_no,omitempty"` // e.g. 2
	Meaning  string `json:"meaning,omitempty"`   // human/agent-readable cause
}

// known maps signal numbers to (name, meaning) for the signals a CI job is
// realistically killed by. The exit code is 128+number.
var known = map[int]struct{ name, meaning string }{
	2:  {"SIGINT", "interrupted (Ctrl-C / SIGINT); Semaphore agent marks the job STOPPED, not FAILED, so failure notifications won't fire"},
	9:  {"SIGKILL", "force-killed: usually out-of-memory (OOM) or hitting a hard resource limit"},
	15: {"SIGTERM", "terminated (SIGTERM): typically a timeout or an external stop request"},
	11: {"SIGSEGV", "segmentation fault: the process crashed"},
	6:  {"SIGABRT", "aborted (SIGABRT): e.g. an assertion failure or panic"},
	1:  {"SIGHUP", "hangup (SIGHUP): controlling terminal closed"},
	13: {"SIGPIPE", "broken pipe (SIGPIPE): wrote to a closed pipe/socket"},
}

// Interpret returns signal Info for a 128+N exit code, or nil when the code
// is a plain application exit (including 0). Returning nil keeps callers
// additive: no signal field is emitted for ordinary failures.
func Interpret(exitCode int) *Info {
	if exitCode <= 128 || exitCode > 165 {
		return nil
	}
	n := exitCode - 128
	info := &Info{ExitCode: exitCode, SignalNo: n}
	if k, ok := known[n]; ok {
		info.Signal = k.name
		info.Meaning = k.meaning
	} else {
		info.Signal = fmt.Sprintf("SIG%d", n)
		info.Meaning = fmt.Sprintf("terminated by signal %d", n)
	}
	return info
}

// Annotate returns a short human-readable suffix for an exit code, e.g.
// " (SIGINT)" for 130, or "" when the code carries no signal meaning.
func Annotate(exitCode int) string {
	if info := Interpret(exitCode); info != nil {
		return " (" + info.Signal + ")"
	}
	return ""
}
