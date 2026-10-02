package dnssrv

import (
	"errors"
	"fmt"
	"testing"
)

// snapshot captures all observable state so rejected ops can be proven to
// change nothing.
func snapshot(s *Selector) map[string]string {
	out := make(map[string]string)
	for k, r := range s.recs {
		out[fmt.Sprintf("%s:%d", k.target, k.port)] = fmt.Sprintf(
			"p=%d w=%d exp=%d reg=%d cool=%d f=%d lf=%d hasLF=%t",
			r.priority, r.weight, r.expire, r.regID, r.coolUntil, r.f, r.lf, r.hasLF)
	}
	out["__nextID"] = itoa(s.nextID)
	return out
}

// TestCapacityEviction follows the spec's Cap=2 example exactly.
func TestCapacityEviction(t *testing.T) {
	s := mustNew(t, 2, 1_000_000_000, 1_000_000_000)

	mustAdd(t, s, "X", 1, 10, 1, 5, 0)   // expire 5, regID 1
	mustAdd(t, s, "Y", 2, 10, 1, 100, 1) // expire 101, regID 2

	// At now=5, X expired: new id Z triggers purge, Z gets fresh regID 3.
	if err := s.Add("Z", 3, 10, 1, 100, 5); err != nil {
		t.Fatalf("Add Z after evicting expired X: %v", err)
	}
	if s.recs[id{"X", 1}] != nil {
		t.Fatal("X should have been evicted")
	}
	if z := s.recs[id{"Z", 3}]; z == nil || z.regID != 3 {
		t.Fatalf("Z state: %+v", z)
	}
	t.Log("判定依据: Cap=2 已满且 Z 新标识, X 到期5<=now=5 先淘汰, Z 以新序号3入库")

	// At now=6 nothing is expired (Y to 101, Z to 105): full -> ErrFull and
	// no record is removed.
	before := snapshot(s)
	err := s.Add("W", 4, 10, 1, 100, 6)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	if got := snapshot(s); !mapsEqual(got, before) {
		t.Fatalf("state changed after ErrFull:\nbefore=%v\nafter =%v", before, got)
	}
	t.Log("判定依据: 淘汰后仍满报 ErrFull, 无任何淘汰发生, 状态不变")

	// Updating an existing id at capacity needs no slot and never purges.
	if err := s.Add("Y", 2, 11, 9, 50, 6); err != nil {
		t.Fatalf("update Y at full capacity: %v", err)
	}
	y := s.recs[id{"Y", 2}]
	if y.priority != 11 || y.weight != 9 || y.expire != 56 || y.regID != 2 {
		t.Fatalf("Y update wrong: %+v", y)
	}
	if len(s.recs) != 2 || s.nextID != 4 {
		t.Fatalf("len=%d nextID=%d, want 2 and 4", len(s.recs), s.nextID)
	}
	t.Log("判定依据: 已满时更新已有 Y 允许, 不淘汰, 登记序号仍 2")
}

// TestPurgeAndReAddRegID: purge removes records; a re-added id gets a new
// registration sequence number.
func TestPurgeAndReAddRegID(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "X", 1, 10, 1, 5, 0)
	mustAdd(t, s, "Y", 2, 10, 1, 100, 0)

	n, err := s.Purge(4)
	if err != nil || n != 0 {
		t.Fatalf("Purge(4)=%d,%v want 0", n, err)
	}
	n, err = s.Purge(5)
	if err != nil || n != 1 {
		t.Fatalf("Purge(5)=%d,%v want 1 (expire boundary <=)", n, err)
	}
	t.Log("判定依据: Purge 删除 expire<=now, now=5 删除 X, 返回1")

	mustAdd(t, s, "X", 1, 10, 1, 100, 6)
	if x := s.recs[id{"X", 1}]; x.regID != 3 {
		t.Fatalf("re-added X regID=%d want 3 (fresh number)", x.regID)
	}
	if s.nextID != 4 {
		t.Fatalf("nextID=%d want 4", s.nextID)
	}
	t.Log("判定依据: 被删除标识再次登记获得新登记序号 3")
}

// TestRejectionsDoNotMutate: every rejected op leaves all state untouched.
func TestRejectionsDoNotMutate(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "X", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "Y", 2, 10, 0, 1000, 0)
	if err := s.Failure("X", 1, 10, 100); err != nil {
		t.Fatal(err)
	}
	before := snapshot(s)

	type op struct {
		name string
		run  func() error
	}
	ops := []op{
		{"add empty target", func() error { return s.Add("", 1, 10, 1, 10, 0) }},
		{"add bad port", func() error { return s.Add("Z", 0, 10, 1, 10, 0) }},
		{"add bad port2", func() error { return s.Add("Z", 70000, 10, 1, 10, 0) }},
		{"add bad priority", func() error { return s.Add("Z", 3, -1, 1, 10, 0) }},
		{"add bad weight", func() error { return s.Add("Z", 3, 10, 70000, 10, 0) }},
		{"add bad ttl", func() error { return s.Add("Z", 3, 10, 1, 0, 0) }},
		{"add bad time", func() error { return s.Add("Z", 3, 10, 1, 10, -1) }},
		{"add full", func() error { return s.Add("Z", 3, 10, 1, 100, 50) }},
		{"failure bad cooldown", func() error { return s.Failure("X", 1, 0, 200) }},
		{"failure bad time", func() error { return s.Failure("X", 1, 10, 1<<50) }},
		{"failure missing", func() error { return s.Failure("Z", 9, 10, 200) }},
		{"failure expired", func() error { return s.Failure("X", 1, 10, 1000) }},
		{"success empty", func() error { return s.Success("", 1, 200) }},
		{"success bad time", func() error { return s.Success("X", 1, -1) }},
		{"success missing", func() error { return s.Success("Z", 9, 200) }},
		{"success expired", func() error { return s.Success("X", 1, 1000) }},
	}
	for _, o := range ops {
		err := o.run()
		if err == nil {
			t.Errorf("%s: expected rejection, got nil", o.name)
		}
		if got := snapshot(s); !mapsEqual(got, before) {
			t.Fatalf("%s mutated state:\nbefore=%v\nafter =%v", o.name, before, got)
		}
		t.Logf("判定依据: 拒绝操作[%s] err=%v, 快照前后一致", o.name, err)
	}

	// Argument error is reported before time error; full after both.
	if err := s.Add("", 3, 10, 1, 10, -1); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("arg before time: got %v", err)
	}
	if err := s.Add("Z", 3, 10, 1, 10, -1); !errors.Is(err, ErrInvalidTime) {
		t.Errorf("want ErrInvalidTime, got %v", err)
	}
	if err := s.Failure("Z", 9, 0, -1); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("failure arg before time: got %v", err)
	}
	if _, _, err := s.Pick(0, -1); !errors.Is(err, ErrInvalidTime) {
		t.Errorf("pick bad time: got %v", err)
	}
	if _, err := s.Purge(1 << 60); !errors.Is(err, ErrInvalidTime) {
		t.Errorf("purge bad time: got %v", err)
	}

	// No available record: both records expired.
	if _, _, err := s.Pick(0, 1000); !errors.Is(err, ErrNoAvailable) {
		t.Errorf("want ErrNoAvailable, got %v", err)
	}
}

func mapsEqual(a, b map[string]string) bool {
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

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
