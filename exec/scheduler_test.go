package exec_test

import (
	"errors"
	"testing"

	"ontology/action"
	"ontology/exec"
)

func plat(kv ...string) action.Platform {
	p := action.Platform{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i]] = kv[i+1]
	}
	return p
}

func receive(s *exec.Scheduler, id int) (action.Outcome, bool) {
	return s.WaiterOutcome(id)
}

func noErr(t *testing.T, err error, where string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", where, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 题给主示例（M=2）：优先级升降、原 seq 归位、跳过不匹配、缓存与 Lost。
func TestSpecExampleMain(t *testing.T) {
	s := exec.New(2)
	must(t, s.Register("W1", plat("os", "linux", "arch", "x86"), 1))

	w1, err := s.Execute("d1", plat("os", "linux"), 1, false)
	noErr(t, err, "execute d1")
	w2, err := s.Execute("d2", plat("os", "win"), 5, false)
	noErr(t, err, "execute d2")
	w3, err := s.Execute("d3", plat("os", "linux"), 1, false)
	noErr(t, err, "execute d3 #1")
	w4, err := s.Execute("d3", plat("os", "linux"), 7, false)
	noErr(t, err, "execute d3 #2 attach")
	if w1 != 1 || w2 != 2 || w3 != 3 || w4 != 4 {
		t.Fatalf("waiter numbering = %d,%d,%d,%d", w1, w2, w3, w4)
	}
	if got := s.QueueOrder(); !equalStr(got, []string{"d3", "d2", "d1"}) {
		t.Fatalf("queue after attach = %v, want d3,d2,d1", got)
	}

	d, attempt, err := s.Poll("W1")
	noErr(t, err, "poll C")
	if d != "d3" || attempt != 1 {
		t.Fatalf("poll = %s/%d, want d3/1", d, attempt)
	}
	noErr(t, s.Cancel(w4), "cancel waiter 4")
	noErr(t, s.WorkerLost("W1"), "worker lost W1")
	if got := s.QueueOrder(); !equalStr(got, []string{"d2", "d1", "d3"}) {
		t.Fatalf("queue after loss = %v, want d2,d1,d3", got)
	}

	must(t, s.Register("W1", plat("os", "linux", "arch", "x86"), 1))
	d, attempt, err = s.Poll("W1")
	noErr(t, err, "poll A skipping B")
	if d != "d1" || attempt != 1 {
		t.Fatalf("poll = %s/%d, want d1/1 (d2 os=win skipped)", d, attempt)
	}
	noErr(t, s.Complete("W1", "d1", 1, 0, false), "complete A exit0")
	if o, ok := receive(s, w1); !ok || o.Kind != "Result" || o.Exit != 0 {
		t.Fatalf("waiter1 = %+v ok=%v, want Result(0)", o, ok)
	}

	d, attempt, err = s.Poll("W1")
	noErr(t, err, "poll C retry")
	if d != "d3" || attempt != 2 {
		t.Fatalf("poll = %s/%d, want d3/2", d, attempt)
	}
	if err := s.Complete("W1", "d3", 1, 0, false); !errors.Is(err, exec.ErrStaleAttempt) {
		t.Fatalf("stale attempt err = %v, want ErrStaleAttempt", err)
	}
	noErr(t, s.WorkerLost("W1"), "worker lost W1 again")
	if o, ok := receive(s, w3); !ok || o.Kind != "Lost" {
		t.Fatalf("waiter3 = %+v ok=%v, want Lost", o, ok)
	}
	if o, ok := receive(s, w4); !ok || o.Kind != "Cancelled" {
		t.Fatalf("waiter4 = %+v ok=%v, want Cancelled", o, ok)
	}

	cached, err := s.Execute("d1", plat("os", "linux"), 0, false)
	noErr(t, err, "cached execute")
	if o, ok := receive(s, cached); !ok || o.Kind != "Cached" || o.Exit != 0 {
		t.Fatalf("cached outcome = %+v ok=%v", o, ok)
	}
	fresh, err := s.Execute("d1", plat("os", "linux"), 0, true)
	noErr(t, err, "skipCache execute")
	if fresh == cached {
		t.Fatalf("skipCache must allocate a new waiter number")
	}
	if o, ok := receive(s, fresh); ok {
		t.Fatalf("fresh execute must not be terminal yet, got %+v", o)
	}
}

// 题给弃置操作示例。
func TestSpecExampleOrphan(t *testing.T) {
	s := exec.New(2)
	must(t, s.Register("W1", plat("os", "linux"), 1))
	w1, _ := s.Execute("dD", plat("os", "linux"), 1, false)
	d, att, _ := s.Poll("W1")
	if d != "dD" || att != 1 {
		t.Fatalf("poll = %s/%d", d, att)
	}
	noErr(t, s.Cancel(w1), "cancel sole assigned waiter")
	if s.UsedSlots() != 1 || s.AssignedCount() != 1 {
		t.Fatalf("orphan op must keep occupying the slot")
	}
	w2, err := s.Execute("dD", plat("os", "linux"), 2, false)
	noErr(t, err, "attach onto orphan op")
	noErr(t, s.Complete("W1", "dD", 1, 3, false), "complete orphan exit3")
	if o, ok := receive(s, w2); !ok || o.Kind != "Result" || o.Exit != 3 {
		t.Fatalf("attached waiter = %+v ok=%v, want Result(3)", o, ok)
	}
	w3, _ := s.Execute("dD", plat("os", "linux"), 0, false)
	if o, ok := receive(s, w3); ok {
		t.Fatalf("exit!=0 must not populate cache, got %+v", o)
	}

	// 第二个 dD 操作派发后取消为弃置，再失联：静默删除。
	d2, _, _ := s.Poll("W1")
	if d2 != "dD" {
		t.Fatalf("poll = %s want dD", d2)
	}
	noErr(t, s.Cancel(w3), "cancel sole waiter of second dD op")
	if s.UsedSlots() != 1 {
		t.Fatalf("orphan should still hold slot")
	}
	noErr(t, s.WorkerLost("W1"), "lost while orphan")
	if s.UsedSlots() != 0 || s.AssignedCount() != 0 || s.InflightCount() != 0 {
		t.Fatalf("orphan must be silently deleted on loss")
	}
}
