package lifecycle

import (
	"fmt"
	"io"
	"sync"
)

// SettleRecord describes one settled ring within a SettleCall.
type SettleRecord struct {
	Instance   string
	Transition string
	From       string
	To         string
	Basis      string
}

// SliceLogger collects every settlement call in memory. Tests use it to
// assert input / advanced rings / decision basis.
type SliceLogger struct {
	mu    sync.Mutex
	Calls []SettleCall
}

// LogSettle implements SettleLogger.
func (l *SliceLogger) LogSettle(call SettleCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Calls = append(l.Calls, call)
}

// CallsCopy returns a snapshot copy of the recorded calls.
func (l *SliceLogger) CallsCopy() []SettleCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SettleCall, len(l.Calls))
	copy(out, l.Calls)
	return out
}

// WriterLogger prints every settlement to an io.Writer as human-readable
// lines: the call input, each advanced ring with its decision basis, and
// any classified error.
type WriterLogger struct {
	mu sync.Mutex
	W  io.Writer
}

// LogSettle implements SettleLogger.
func (l *WriterLogger) LogSettle(call SettleCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.W, "[settle] call=%s instance=%s now=%s rings=%d\n",
		call.Caller, call.Instance, call.Now.Format("15:04:05.000"), len(call.Rings))
	for i, r := range call.Rings {
		fmt.Fprintf(l.W, "          #%d %s: %s %s -> %s | basis: %s\n",
			i+1, r.Instance, r.Transition, r.From, r.To, r.Basis)
	}
	if call.Err != "" {
		fmt.Fprintf(l.W, "          error: %s\n", call.Err)
	}
}
