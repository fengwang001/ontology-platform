package replay

import (
	"fmt"
	"strings"
	"testing"

	"ontology/code"
	"ontology/history"
)

// evString 把事件渲染成紧凑记号 S(name)/M(pid)，用于日志与比较。
func evString(e history.Event) string {
	if e.IsStep() {
		return fmt.Sprintf("S(%s)", string(e.Name))
	}
	return fmt.Sprintf("M(%s)", string(e.Pid))
}

func evsString(es []history.Event) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = evString(e)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func codeString(program code.Code) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, it := range program {
		if i > 0 {
			b.WriteString(", ")
		}
		if it.IsStep() {
			b.WriteString("Step " + string(it.Name))
			continue
		}
		b.WriteString(fmt.Sprintf("Branch(%s, N=%s, O=%s)",
			string(it.Pid), stepsString(it.New), stepsString(it.Old)))
	}
	b.WriteByte(']')
	return b.String()
}

func stepsString(items []code.Item) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = string(it.Name)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func mustEqualEvents(t *testing.T, got []history.Event, want []history.Event, ctx string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %s, want %s", ctx, evsString(got), evsString(want))
	}
	for i := range want {
		if evString(got[i]) != evString(want[i]) {
			t.Fatalf("%s: got %s, want %s", ctx, evsString(got), evsString(want))
		}
	}
}

// seed 直接把事件装入一个新 Store（测试用，绕过 Append 的 expect）。
func seed(t *testing.T, wf string, evs []history.Event) (*history.Store, []byte) {
	t.Helper()
	s := &history.Store{}
	if len(evs) > 0 {
		if err := s.Append([]byte(wf), 0, evs); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return s, []byte(wf)
}
