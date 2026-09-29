package replica

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestSet(t *testing.T, tolerance time.Duration) (*SyncSet, *bytes.Buffer, time.Time) {
	t.Helper()
	var logBuf bytes.Buffer
	start := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	s, err := NewSyncSet("leader", tolerance, start, WithLogWriter(&logBuf))
	if err != nil {
		t.Fatalf("NewSyncSet: %v", err)
	}
	return s, &logBuf, start
}

func mustAdd(t *testing.T, s *SyncSet, id string) {
	t.Helper()
	if err := s.AddReplica(id); err != nil {
		t.Fatalf("AddReplica(%q): %v", id, err)
	}
}

func mustAppend(t *testing.T, s *SyncSet, now time.Time, n int64) (int64, int64) {
	t.Helper()
	end, hwm, err := s.Append(now, n)
	if err != nil {
		t.Fatalf("Append(%d): %v", n, err)
	}
	return end, hwm
}

func mustFetch(t *testing.T, s *SyncSet, now time.Time, id string, end int64) {
	t.Helper()
	if err := s.Fetch(now, id, end); err != nil {
		t.Fatalf("Fetch(%q,%d): %v", id, end, err)
	}
}

func TestHWMAdvancesWithMinSyncedEnd(t *testing.T) {
	s, _, t0 := newTestSet(t, time.Minute)
	mustAdd(t, s, "f1")
	mustAdd(t, s, "f2")
	// 初始高水位 0，跟随者首次拉取 0 即追平，进入同步副本集。
	mustFetch(t, s, t0.Add(time.Second), "f1", 0)
	mustFetch(t, s, t0.Add(2*time.Second), "f2", 0)
	if got := s.Status().Synced; len(got) != 3 {
		t.Fatalf("synced = %v, want leader+f1+f2", got)
	}

	mustAppend(t, s, t0.Add(3*time.Second), 10)
	if st := s.Status(); st.HighWatermark != 0 {
		t.Fatalf("hwm = %d, want 0 (no follower fetched)", st.HighWatermark)
	}
	mustFetch(t, s, t0.Add(4*time.Second), "f1", 10)
	if st := s.Status(); st.HighWatermark != 0 {
		t.Fatalf("hwm = %d, want 0 (f2 still at 0)", st.HighWatermark)
	}
	mustFetch(t, s, t0.Add(5*time.Second), "f2", 10)
	if st := s.Status(); st.HighWatermark != 10 {
		t.Fatalf("hwm = %d, want 10", st.HighWatermark)
	}
}

func TestSweepRemovesLaggingAndRejoin(t *testing.T) {
	tol := 10 * time.Second
	s, logBuf, t0 := newTestSet(t, tol)
	mustAdd(t, s, "f1")
	mustFetch(t, s, t0.Add(time.Second), "f1", 0)

	mustAppend(t, s, t0.Add(2*time.Second), 5)
	mustFetch(t, s, t0.Add(3*time.Second), "f1", 5)
	if hwm := s.Status().HighWatermark; hwm != 5 {
		t.Fatalf("hwm = %d, want 5", hwm)
	}

	// f1 在 t0+3s 追平后不再拉取。lag 恰好等于 tolerance 时保留。
	mustAppend(t, s, t0.Add(4*time.Second), 5)
	removed, err := s.Sweep(t0.Add(3*time.Second + tol))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed at exact tolerance = %v, want none", removed)
	}

	// 再多 1ns：严格大于阈值，f1 被移出；ISR 仅剩 leader(end=10)。
	removed, err = s.Sweep(t0.Add(3*time.Second + tol + time.Nanosecond))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 1 || removed[0] != "f1" {
		t.Fatalf("removed = %v, want [f1]", removed)
	}
	st := s.Status()
	if st.HighWatermark != 10 {
		t.Fatalf("hwm after removal = %d, want 10", st.HighWatermark)
	}
	if len(st.Synced) != 1 || st.Synced[0] != "leader" {
		t.Fatalf("synced = %v, want [leader]", st.Synced)
	}
	if st.Ends["f1"] != 5 {
		t.Fatalf("f1 end = %d, want 5 (progress preserved)", st.Ends["f1"])
	}

	// 拉到 8（< 当前高水位 10）：不能重新加入。
	mustFetch(t, s, t0.Add(20*time.Second), "f1", 8)
	if synced := s.Status().Synced; len(synced) != 1 {
		t.Fatalf("synced = %v, f1 must not rejoin below hwm", synced)
	}
	// 追平领导者（10==hwm）：重新加入。
	mustFetch(t, s, t0.Add(21*time.Second), "f1", 10)
	if synced := s.Status().Synced; len(synced) != 2 || synced[1] != "f1" {
		t.Fatalf("synced = %v, want f1 rejoined", synced)
	}
	if !strings.Contains(logBuf.String(), "decision=remove iff lag strictly greater") {
		t.Fatalf("log missing sweep decision:\n%s", logBuf.String())
	}
}

func TestHWMMonotonicAcrossRemoval(t *testing.T) {
	tol := time.Second
	s, _, t0 := newTestSet(t, tol)
	mustAdd(t, s, "f1")
	mustFetch(t, s, t0, "f1", 0)
	mustAppend(t, s, t0, 3)
	mustFetch(t, s, t0, "f1", 3)
	mustAppend(t, s, t0, 7) // leader=10, f1=3, hwm 保持 3
	if hwm := s.Status().HighWatermark; hwm != 3 {
		t.Fatalf("hwm = %d, want 3", hwm)
	}
	if _, err := s.Sweep(t0.Add(tol + time.Nanosecond)); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if hwm := s.Status().HighWatermark; hwm != 10 {
		t.Fatalf("hwm = %d, want 10 (ISR only leader)", hwm)
	}
	// 落后位点的拉取绝不能把 hwm 拉低。
	mustFetch(t, s, t0.Add(2*tol), "f1", 5)
	mustFetch(t, s, t0.Add(3*tol), "f1", 10)
	if hwm := s.Status().HighWatermark; hwm != 10 {
		t.Fatalf("hwm = %d, must stay 10", hwm)
	}
}

func statusEqual(a, b Status) bool {
	if a.Leader != b.Leader || a.HighWatermark != b.HighWatermark ||
		!a.LastClock.Equal(b.LastClock) || len(a.Synced) != len(b.Synced) {
		return false
	}
	for i := range a.Synced {
		if a.Synced[i] != b.Synced[i] {
			return false
		}
	}
	if len(a.Ends) != len(b.Ends) {
		return false
	}
	for k, v := range a.Ends {
		if b.Ends[k] != v {
			return false
		}
	}
	return true
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	tol := time.Minute
	s, _, t0 := newTestSet(t, tol)
	mustAdd(t, s, "f1")
	mustFetch(t, s, t0.Add(time.Second), "f1", 0)
	mustAppend(t, s, t0.Add(2*time.Second), 4)
	mustFetch(t, s, t0.Add(3*time.Second), "f1", 2)

	before := s.Status()
	type tc struct {
		name string
		want error
		run  func() error
	}
	cases := []tc{
		{"append negative n", ErrInvalidArgument, func() error {
			_, _, err := s.Append(t0.Add(4*time.Second), -1)
			return err
		}},
		{"append zero time", ErrInvalidArgument, func() error {
			_, _, err := s.Append(time.Time{}, 1)
			return err
		}},
		{"append clock rollback", ErrClockRollback, func() error {
			_, _, err := s.Append(t0.Add(time.Second), 1)
			return err
		}},
		{"fetch empty id", ErrInvalidArgument, func() error {
			return s.Fetch(t0.Add(4*time.Second), "", 4)
		}},
		{"fetch unknown replica", ErrUnknownReplica, func() error {
			return s.Fetch(t0.Add(4*time.Second), "ghost", 0)
		}},
		{"fetch negative offset", ErrInvalidOffset, func() error {
			return s.Fetch(t0.Add(4*time.Second), "f1", -1)
		}},
		{"fetch ahead of leader", ErrInvalidOffset, func() error {
			return s.Fetch(t0.Add(4*time.Second), "f1", 5)
		}},
		{"fetch regressed offset", ErrInvalidOffset, func() error {
			return s.Fetch(t0.Add(4*time.Second), "f1", 1)
		}},
		{"fetch by leader", ErrInvalidArgument, func() error {
			return s.Fetch(t0.Add(4*time.Second), "leader", 4)
		}},
		{"sweep zero time", ErrInvalidArgument, func() error {
			_, err := s.Sweep(time.Time{})
			return err
		}},
		{"sweep clock rollback", ErrClockRollback, func() error {
			_, err := s.Sweep(t0)
			return err
		}},
		{"add duplicate", ErrInvalidArgument, func() error { return s.AddReplica("f1") }},
		{"add empty id", ErrInvalidArgument, func() error { return s.AddReplica("") }},
		{"new empty leader", ErrInvalidArgument, func() error {
			_, err := NewSyncSet("", tol, t0)
			return err
		}},
		{"new negative tolerance", ErrInvalidArgument, func() error {
			_, err := NewSyncSet("l", -1, t0)
			return err
		}},
		{"new zero start", ErrInvalidArgument, func() error {
			_, err := NewSyncSet("l", tol, time.Time{})
			return err
		}},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			seen[c.want] = true
			after := s.Status()
			if !statusEqual(before, after) {
				t.Fatalf("state changed after rejected %q:\nbefore=%+v\nafter =%+v", c.name, before, after)
			}
		})
	}
	if len(seen) != 4 {
		t.Fatalf("expected all 4 distinct error classes exercised, got %d", len(seen))
	}
}
