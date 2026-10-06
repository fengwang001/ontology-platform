package kvlog

import "os"

// crashExit simulates an immediate power loss. Isolated for tests.
var crashExit = func() { os.Exit(42) }

// SetCrashHook installs a crash injection hook (test support). Returning true
// triggers a simulated immediate exit at the named point.
func (e *Engine) SetCrashHook(h func(point string) bool) {
	e.mu.Lock()
	e.crashHook = h
	e.mu.Unlock()
}
