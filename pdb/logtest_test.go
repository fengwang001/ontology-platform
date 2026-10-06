package pdb

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// opLogger mirrors every operation's input, output and the reason behind the
// decision both to t.Log (visible via `go test -v`) and to
// testlogs/<TestName>.log for post-run inspection.
type opLogger struct {
	t   *testing.T
	mu  sync.Mutex
	f   *os.File
	seq int
}

func newOpLogger(t *testing.T) *opLogger {
	t.Helper()
	dir := filepath.Join("..", "testlogs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir testlogs: %v", err)
	}
	path := filepath.Join(dir, t.Name()+".log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return &opLogger{t: t, f: f}
}

func (l *opLogger) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	line := fmt.Sprintf("[%04d] %s\n", l.seq, fmt.Sprintf(format, args...))
	l.f.WriteString(line)
	l.t.Logf("%s", line[:len(line)-1])
}

func errStr(err error) string {
	if err == nil {
		return "ALLOW"
	}
	return fmt.Sprintf("DENY(%s: %s)", ReasonName(ErrReason(err)), err.Error())
}

// ReasonName maps a Reason to its canonical English name.
func ReasonName(r Reason) string {
	switch r {
	case ReasonInvalidArgument:
		return "InvalidArgument"
	case ReasonClockRollback:
		return "ClockRollback"
	case ReasonPodNotFound:
		return "PodNotFound"
	case ReasonNotEvictablePhase:
		return "NotEvictablePhase"
	case ReasonAlreadyEvicting:
		return "AlreadyEvicting"
	case ReasonConflict:
		return "ConfigConflict"
	case ReasonInsufficient:
		return "Insufficient"
	default:
		return fmt.Sprintf("Unknown(%d)", r)
	}
}
