package disruption

import (
	"fmt"
	"testing"
)

// opLogger records every operation: inputs, actual output and the decision
// basis. Tests call logf at each step; with -v the lines are streamed, and on
// failure the full per-step rationale is visible.
type opLogger struct {
	t   *testing.T
	seq int
}

func newOpLogger(t *testing.T) *opLogger {
	return &opLogger{t: t}
}

func (l *opLogger) logf(format string, args ...any) {
	l.seq++
	l.t.Logf("op#%03d %s", l.seq, fmt.Sprintf(format, args...))
}

func mustKind(t *testing.T, err error, want Kind) {
	t.Helper()
	got, ok := KindOf(err)
	if !ok {
		t.Fatalf("want adjudication error kind %s, got %v", want, err)
	}
	if got != want {
		t.Fatalf("want error kind %s, got %s", want, got)
	}
}

func pct(v int) *IntOrPct { return &IntOrPct{Value: v, Percent: true} }
func abs(v int) *IntOrPct { return &IntOrPct{Value: v, Percent: false} }

func pod(ns, name string, phase Phase, ready bool, labels map[string]string) Pod {
	return Pod{ID: PodID{Namespace: ns, Name: name}, Phase: phase, Ready: ready, Labels: labels}
}
