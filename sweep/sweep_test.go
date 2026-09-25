package sweep

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"ontology/hb"
)

// Invariant 1: lastHb is monotonic; stale heartbeats are ignored.
func TestHeartbeatMonotonic(t *testing.T) {
	cases := []struct {
		name string
		seq  []int64
		want int64
	}{
		{"increasing", []int64{0, 5, 40}, 40},
		{"stale ignored", []int64{40, 20, 39}, 40},
		{"equal accepted", []int64{7, 7}, 7},
		{"stale then newer", []int64{40, 20, 41}, 41},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New()
			for _, ts := range c.seq {
				_ = m.Heartbeat("s", ts) // ErrStale itself is pinned via SelfCheck
			}
			if last, _ := m.streams["s"].Last(); last != c.want {
				t.Fatalf("lastHb=%d want %d", last, c.want)
			}
		})
	}
}

// Invariant 3: age thresholds drive active->idle->dead (left-closed).
func TestTransitions(t *testing.T) {
	m := New()
	if err := m.Heartbeat("s", 0); err != nil {
		t.Fatal(err)
	}
	type q struct {
		now  int64
		want hb.State
	}
	for _, qq := range []q{{10, hb.Active}, {11, hb.Idle}, {30, hb.Idle}, {31, hb.Dead}, {100, hb.Dead}} {
		if got, err := m.Status("s", qq.now); err != nil || got != qq.want {
			t.Fatalf("now=%d: got %v,%v want %v", qq.now, got, err, qq.want)
		}
	}
}

// Invariant 3 (recovery): a dead stream accepts a heartbeat and is active.
func TestRecovery(t *testing.T) {
	m := New()
	_ = m.Heartbeat("s", 0)
	if got, _ := m.Status("s", 31); got != hb.Dead {
		t.Fatalf("precondition: got %v want dead", got)
	}
	if err := m.Heartbeat("s", 40); err != nil {
		t.Fatalf("recovery heartbeat rejected: %v", err)
	}
	if got, _ := m.Status("s", 40); got != hb.Active {
		t.Fatalf("after recovery: got %v want active", got)
	}
	if ids, _ := m.Sweep(40); len(ids) != 0 {
		t.Fatalf("recovered stream still swept: %v", ids)
	}
}

// Invariant 4: rejected operations change nothing and stay distinguishable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	m := New()
	_ = m.Heartbeat("s", 10)
	before := m.View()
	ops := []struct {
		name string
		err  error
		want error
	}{
		{"hb empty id", m.Heartbeat("", 1), ErrEmptyID},
		{"hb negative ts", m.Heartbeat("s", -1), hb.ErrNegativeTime},
		{"hb negative ts new stream", m.Heartbeat("ghost", -1), hb.ErrNegativeTime},
		{"status empty id", func() error { _, e := m.Status("", 10); return e }(), ErrEmptyID},
		{"status negative now", func() error { _, e := m.Status("s", -1); return e }(), hb.ErrNegativeTime},
		{"status clock back", func() error { _, e := m.Status("s", 9); return e }(), hb.ErrClockBack},
		{"sweep negative now", func() error { _, e := m.Sweep(-1); return e }(), hb.ErrNegativeTime},
	}
	for _, o := range ops {
		if !errors.Is(o.err, o.want) {
			t.Fatalf("%s: got %v want %v", o.name, o.err, o.want)
		}
	}
	if !reflect.DeepEqual(m.View(), before) {
		t.Fatalf("view changed after rejections: %v", m.View())
	}
	if _, ok := m.streams["ghost"]; ok {
		t.Fatal("rejected heartbeat created a stream")
	}
	if got, _ := m.Status("s", 15); got != hb.Active {
		t.Fatalf("detector unusable after rejections: %v", got)
	}
}

// Invariant 2: Sweep equals the naive reference, and is idempotent.
func TestSweepMatchesNaive(t *testing.T) {
	cases := []struct {
		name string
		last map[string]int64
		now  int64
	}{
		{"none dead", map[string]int64{"a": 0, "b": 5}, 10},
		{"boundary 30 idle", map[string]int64{"a": 0}, 30},
		{"boundary 31 dead", map[string]int64{"a": 0}, 31},
		{"mixed", map[string]int64{"a": 0, "b": 5, "c": 40, "d": 11}, 41},
	}
	for _, c := range cases {
		m := New()
		want := []string{}
		for id, ts := range c.last {
			_ = m.Heartbeat(id, ts)
			if c.now-ts > hb.IdleWindow {
				want = append(want, id)
			}
		}
		sort.Strings(want)
		for rep := 0; rep < 2; rep++ { // Sweep must be repeatable
			got, err := m.Sweep(c.now)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s rep=%d got %v,%v want %v", c.name, rep, got, err, want)
			}
		}
	}
}

// Complexity: streams examined by Sweep stays bounded as N grows.
func TestSweepCheckedBounded(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		m := New()
		for i := 0; i < n; i++ {
			_ = m.Heartbeat(fmt.Sprintf("s%d", i), 0) // all alive
		}
		_, err := m.Sweep(5)
		if err != nil || m.checked > 2 {
			t.Fatalf("N=%d: checked=%d err=%v", n, m.checked, err)
		}
	}
}
