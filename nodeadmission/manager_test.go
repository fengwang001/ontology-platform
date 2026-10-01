package nodeadmission

import (
	"errors"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func errCode(err error) RejectCode {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Code
	}
	return OK
}

func mustOK(t *testing.T, err error, log *strings.Builder, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v\n%s", what, err, log.String())
	}
}

func expectCode(t *testing.T, err error, want RejectCode, log *strings.Builder, what string) {
	t.Helper()
	if got := errCode(err); got != want {
		t.Fatalf("%s: want code %d got %v (%v)\n%s", what, want, got, err, log.String())
	}
}

func neTol(key, op, value, effect string, sec int64) Toleration {
	return Toleration{Key: key, Operator: op, Value: value, Effect: effect, Seconds: sec}
}

// Eviction happens exactly at addedAt+seconds*1000; one ms early it does not.
func TestExactBoundaryOneMs(t *testing.T) {
	var log strings.Builder
	m, err := NewManager(0, 10)
	mustOK(t, err, &log, "NewManager")
	mustOK(t, m.AddNode("n", 10), &log, "AddNode")
	mustOK(t, m.Taint("n", Taint{"k", "v", NoExecute}, 0), &log, "Taint")
	mustOK(t, m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 10)}, 5), &log, "Schedule")

	got9, err := m.Tick(9999)
	mustOK(t, err, &log, "Tick9999")
	t.Logf("输入 Tick(9999); 输出 %v; 判定: addedAt+10s*1000=10000 > now, 不驱逐", got9)
	if len(got9) != 0 {
		t.Fatalf("1ms early must not evict, got %v\n%s", got9, log.String())
	}
	got10, err := m.Tick(10000)
	mustOK(t, err, &log, "Tick10000")
	t.Logf("输入 Tick(10000); 输出 %v; 判定: 到期值恰等于 now, 驱逐", got10)
	if len(got10) != 1 || got10[0] != "p" {
		t.Fatalf("exact boundary must evict, got %v", got10)
	}
}

// Timing starts from taint addedAt, not pod bind time.
func TestTimingFromAddedAtNotBind(t *testing.T) {
	m, _ := NewManager(0, 10)
	if err := m.AddNode("n", 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Taint("n", Taint{"k", "v", NoExecute}, 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 10)}, 5000); err != nil {
		t.Fatal(err)
	}
	got, err := m.Tick(11000)
	if err != nil || len(got) != 1 {
		t.Fatalf("eviction derives from addedAt (1000+10000=11000): %v %v", got, err)
	}
}

// First matching toleration in declaration order wins regardless of seconds.
func TestFirstMatchingTolerationWins(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 0)
	m1, _ := NewManager(0, 10)
	_ = m1.AddNode("n", 10)
	_ = m1.Taint("n", Taint{"k", "v", NoExecute}, 0)

	_ = m.Schedule("p", "n", []Toleration{
		neTol("k", OpEqual, "v", NoExecute, 30),
		neTol("k", OpEqual, "v", NoExecute, -1),
	}, 0)
	got, _ := m.Tick(30000)
	if len(got) != 1 {
		t.Fatalf("30s first => evict at 30s, got %v", got)
	}

	_ = m1.Schedule("p", "n", []Toleration{
		neTol("k", OpEqual, "v", NoExecute, -1),
		neTol("k", OpEqual, "v", NoExecute, 30),
	}, 0)
	got1, _ := m1.Tick(1 << 40)
	if len(got1) != 0 {
		t.Fatalf("infinite first => never evict, got %v", got1)
	}
}

// Minimum over several NoExecute taint deadlines.
func TestMinAcrossTaints(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"a", "v", NoExecute}, 0)
	_ = m.Taint("n", Taint{"b", "v", NoExecute}, 0)
	_ = m.Schedule("p", "n", []Toleration{
		neTol("a", OpEqual, "v", NoExecute, 100),
		neTol("b", OpEqual, "v", NoExecute, 5),
	}, 0)
	got, _ := m.Tick(4000)
	if len(got) != 0 {
		t.Fatalf("at 4s not yet due, got %v", got)
	}
	got, _ = m.Tick(5000)
	if len(got) != 1 {
		t.Fatalf("min(100s,5s)=5s, got %v", got)
	}
}

// Untolerated NoExecute taint => d = addedAt.
func TestUntoleratedNoExecuteDueAtAddedAt(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Schedule("p", "n", nil, 50)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 100)
	got, _ := m.Tick(100)
	if len(got) != 1 {
		t.Fatalf("untolerated taint d=addedAt, got %v", got)
	}
}

// Seconds == 0 evicts at addedAt.
func TestSecondsZero(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 7)
	_ = m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 0)}, 8)
	got, _ := m.Tick(8)
	if len(got) != 1 {
		t.Fatalf("seconds=0 => d=addedAt=7 <= 8, got %v", got)
	}
}

// Replacing the value keeps addedAt; an Equal toleration stops matching and
// the pod becomes due at the old addedAt immediately.
func TestReplaceValueKeepsAddedAt(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"k", "v1", NoExecute}, 0)
	_ = m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v1", NoExecute, 1000)}, 0)
	got, _ := m.Tick(100)
	if len(got) != 0 {
		t.Fatalf("still tolerated, got %v", got)
	}
	_ = m.Taint("n", Taint{"k", "v2", NoExecute}, 100)
	got, _ = m.Tick(100)
	if len(got) != 1 {
		t.Fatalf("Equal(v1) no longer matches, d=addedAt=0, got %v", got)
	}
}

// Untaint then re-add restarts timing at the new now.
func TestUntaintReaddedRestarts(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 0)
	_ = m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 10)}, 0)
	got, _ := m.Tick(5000)
	if len(got) != 0 {
		t.Fatalf("not yet due, got %v", got)
	}
	_ = m.Untaint("n", "k", NoExecute, 6000)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 10000)
	got, _ = m.Tick(15000)
	if len(got) != 0 {
		t.Fatalf("readded taint restarts, d=20000, got %v", got)
	}
	got, _ = m.Tick(20000)
	if len(got) != 1 {
		t.Fatalf("due again at 20000, got %v", got)
	}
}

// Rate limit spreads evictions; Untaint between ticks rescues a due pod.
func TestRateLimitAndRescueByUntaint(t *testing.T) {
	m, _ := NewManager(0, 1)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"a", "v", NoExecute}, 0)
	_ = m.Taint("n", Taint{"b", "v", NoExecute}, 0)
	_ = m.Schedule("p1", "n", []Toleration{
		neTol("a", OpEqual, "v", NoExecute, 0),
		neTol("b", OpEqual, "v", NoExecute, -1),
	}, 0)
	_ = m.Schedule("p2", "n", []Toleration{
		neTol("a", OpEqual, "v", NoExecute, -1),
		neTol("b", OpEqual, "v", NoExecute, 0),
	}, 0)

	got, _ := m.Tick(10)
	if len(got) != 1 || got[0] != "p1" {
		t.Fatalf("rate=1 evicts only p1 first, got %v", got)
	}
	_ = m.Untaint("n", "b", NoExecute, 20)
	got, _ = m.Tick(20)
	if len(got) != 0 {
		t.Fatalf("p2 rescued by Untaint, got %v", got)
	}
}

// releaseAt == now frees the slot; one ms earlier the slot is still held.
// G == 0 releases immediately and frees the pod ID.
func TestGraceBoundaryAndGZero(t *testing.T) {
	var log strings.Builder
	m, _ := NewManager(1000, 10)
	_ = m.AddNode("n", 1)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 0)
	_ = m.Schedule("p1", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 0)}, 5)
	got, _ := m.Tick(10)
	t.Logf("输入 Tick(10); 输出 %v; 判定: p1 进入终止中, releaseAt=1010", got)

	expectCode(t, m.Schedule("p2", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, -1)}, 1009), ErrCapacity, &log, "Schedule at 1009")
	mustOK(t, m.Schedule("p2", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, -1)}, 1010), &log, "Schedule at 1010: releaseAt==now frees slot")

	m0, _ := NewManager(0, 10)
	_ = m0.AddNode("n", 1)
	_ = m0.Taint("n", Taint{"k", "v", NoExecute}, 0)
	_ = m0.Schedule("p1", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 0)}, 0)
	g0, _ := m0.Tick(1)
	if len(g0) != 1 {
		t.Fatalf("G=0 tick evicts p1, got %v", g0)
	}
	if err := m0.Schedule("p1", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, -1)}, 1); err != nil {
		t.Fatalf("G=0 releases immediately and frees the ID: %v", err)
	}
}

// Terminating pods occupy capacity and block rebinding the same ID.
func TestTerminatingBlocksCapacityAndID(t *testing.T) {
	m, _ := NewManager(10000, 10)
	_ = m.AddNode("n", 1)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 0)
	_ = m.Schedule("p1", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, 0)}, 0)
	_, _ = m.Tick(1)

	if err := m.Schedule("p2", "n", []Toleration{neTol("k", OpEqual, "v", NoExecute, -1)}, 2); errCode(err) != ErrCapacity {
		t.Fatalf("terminating pod occupies the only slot, got %v", err)
	}
	if err := m.Schedule("p1", "n", nil, 2); errCode(err) != ErrPodExists {
		t.Fatalf("same ID while terminating must be ErrPodExists, got %v", err)
	}
}

// Empty key + Exists matches any key.
func TestEmptyKeyWildcard(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 10)
	_ = m.Taint("n", Taint{"anything", "v", NoExecute}, 0)
	if err := m.Schedule("p", "n", []Toleration{neTol("", OpExists, "", NoExecute, -1)}, 0); err != nil {
		t.Fatalf("empty-key Exists tolerates any key: %v", err)
	}
	got, _ := m.Tick(1 << 40)
	if len(got) != 0 {
		t.Fatalf("wildcard infinite toleration never evicts, got %v", got)
	}
}

var _ = sort.Strings
var _ = rand.Int
