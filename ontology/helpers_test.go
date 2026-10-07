package ontology

import (
	"fmt"
	"strings"
	"testing"
)

// testLogger 同时输出到 t.Log（go test -v 可见）与标准输出，
// 满足「打印输入、实际输出与据以判定的依据」的要求。
type testLogger struct{ t *testing.T }

func (l testLogger) log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.t.Log(msg)
	fmt.Println(msg)
}

func stateString(m map[string]string) string {
	keys := sortedKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func eventsString(ev []ChangeEvent) string {
	parts := make([]string, 0, len(ev))
	for _, e := range ev {
		if e.Action != nil {
			parts = append(parts, fmt.Sprintf("#%d %s %s:%s->%s",
				e.Seq, e.Outcome, e.Action.Instance, e.Action.Before, e.Action.After))
		} else {
			parts = append(parts, fmt.Sprintf("#%d %s(%s, no-state-change)", e.Seq, e.Kind, e.Outcome))
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func mustState(t *testing.T, got, want map[string]string, basis string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("STATE MISMATCH [%s]: got %s want %s", basis, stateString(got), stateString(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("STATE MISMATCH [%s] at %s: got %s want %s | full got=%s",
				basis, k, got[k], v, stateString(got))
		}
	}
}

func sameEvents(a, b []ChangeEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Seq != y.Seq || x.Kind != y.Kind || x.Outcome != y.Outcome {
			return false
		}
		if (x.Action == nil) != (y.Action == nil) {
			return false
		}
		if x.Action != nil && *x.Action != *y.Action {
			return false
		}
	}
	return true
}

func newSeeded(t *testing.T, instances ...string) (*AuditStore, *Executor) {
	t.Helper()
	store := NewAuditStore()
	for i, inst := range instances {
		if err := store.Seed(inst, fmt.Sprintf("v0-%d", i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec, err := NewExecutor(store)
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	return store, exec
}
