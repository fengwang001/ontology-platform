package audit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func timeNowNano() int64 { return time.Now().UnixNano() }

// testLogger 累积每个用例的输入、实际输出与判定依据，并在结束时打印。
type testLogger struct{ sb strings.Builder }

func (l *testLogger) logf(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func (l *testLogger) flush(t *testing.T) {
	t.Helper()
	t.Logf("\n%s", l.sb.String())
}

func newSystem(snapshotInterval int) (*StateStore, *AuditLog, *Executor, *Replayer, *NaiveModel) {
	store := NewStateStore()
	log := NewAuditLog()
	exec := NewExecutor(store, log)
	replayer := NewReplayer(log, snapshotInterval)
	naive := NewNaiveModel(log)
	return store, log, exec, replayer, naive
}

func mustRegister(t *testing.T, exec *Executor, typ, inst, val string) {
	t.Helper()
	if _, err := exec.RegisterInstance(typ, inst, val); err != nil {
		t.Fatalf("register instance: %v", err)
	}
}

func assertState(t *testing.T, got, want map[string]string, basis string) {
	t.Helper()
	if !stateEqual(got, want) {
		t.Fatalf("state mismatch (%s)\n got=%v\nwant=%v", basis, got, want)
	}
}

func stateEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func isIllegal(err error) bool {
	var e *IllegalRequestError
	return errors.As(err, &e)
}

func isAuditWrite(err error) bool {
	var e *AuditWriteError
	return errors.As(err, &e)
}
