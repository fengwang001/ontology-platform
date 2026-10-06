package initsession

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// tlog prints every test input, output and the reasoning behind it to
// both the test log (visible with `go test -v`) and a file under
// testdata/logs, so a run can be audited after the fact. All writes are
// serialized: the concurrent test interleaves registrations and solves
// from many goroutines and every line must stay intact.
type testLogger struct {
	t  testing.TB
	mu sync.Mutex
	f  *os.File
}

var (
	logOnce sync.Once
	logFile *os.File
	logErr  error
)

func newTestLogger(t testing.TB) *testLogger {
	logOnce.Do(func() {
		if err := os.MkdirAll("testdata/logs", 0o755); err != nil {
			logErr = err
			return
		}
		logFile, logErr = os.Create("testdata/logs/initsession-trace.log")
	})
	l := &testLogger{t: t}
	if logErr != nil {
		return l
	}
	l.f = logFile
	return l
}

func (l *testLogger) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.t.Log(msg)
	if l.f != nil {
		fmt.Fprintln(l.f, msg)
	}
}

func (l *testLogger) inputf(format string, args ...any) {
	l.logf("  INPUT  "+format, args...)
}

func (l *testLogger) outputf(format string, args ...any) {
	l.logf("  OUTPUT "+format, args...)
}

func (l *testLogger) reasonf(format string, args ...any) {
	l.logf("  REASON "+format, args...)
}
