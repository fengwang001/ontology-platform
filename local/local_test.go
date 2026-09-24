package local

import (
	"errors"
	"fmt"
	"testing"
)

// TestLocalFeed pins the eight NOTES.md batches step by step at buffer level.
func TestLocalFeed(t *testing.T) {
	type step struct {
		in    []Event
		push  []Event // expected pushes this batch
		pend  map[string]int64
		pendN map[string]int64
		hot   []string
	}
	steps := []step{
		{[]Event{{"A", 5}}, nil, map[string]int64{"A": 5}, map[string]int64{"A": 1}, nil},
		{[]Event{{"A", 3}}, nil, map[string]int64{"A": 8}, map[string]int64{"A": 2}, nil},
		{[]Event{{"A", 2}}, []Event{{"A", 10}}, nil, nil, []string{"A"}},
		{[]Event{{"A", -4}}, []Event{{"A", -4}}, nil, nil, []string{"A"}},
		{[]Event{{"B", 7}, {"B", 2}}, nil, map[string]int64{"B": 9}, map[string]int64{"B": 2}, []string{"A", "B"}},
		{[]Event{{"A", 1}, {"B", 1}}, []Event{{"A", 1}, {"B", 1}}, map[string]int64{"B": 9}, map[string]int64{"B": 2}, []string{"A", "B"}},
		{[]Event{{"C", 2}, {"C", 3}, {"C", 1}}, []Event{{"C", 6}}, map[string]int64{"B": 9}, map[string]int64{"B": 2}, []string{"A", "B", "C"}},
		{[]Event{{"C", 1}}, []Event{{"C", 1}}, map[string]int64{"B": 9}, map[string]int64{"B": 2}, []string{"A", "B", "C"}},
	}
	b := NewBuffer(3)
	for i, s := range steps {
		got, err := b.Feed(s.in)
		if err != nil || !eqEvents(got, s.push) {
			t.Fatalf("step %d: pushes=%v err=%v, want %v", i+1, got, err, s.push)
		}
		for k, sum := range s.pend {
			e, ok := b.pending[k]
			if !ok || e.sum != sum || e.n != s.pendN[k] {
				t.Fatalf("step %d: pending[%s]={%d,%d}, want sum %d n %d", i+1, k, e.sum, e.n, sum, s.pendN[k])
			}
		}
		if len(b.pending) != len(s.pend) || !eqStr(b.Hot(), s.hot) {
			t.Fatalf("step %d: pending=%v hot=%v, want pend %v hot %v", i+1, b.PendingAll(), b.Hot(), s.pend, s.hot)
		}
	}
	got := b.FlushAll() // only B has a buffered sum; A,C stay hot
	if !eqEvents(got, []Event{{"B", 9}}) || !eqStr(b.Hot(), []string{"A", "C"}) {
		t.Fatalf("FlushAll: %v hot=%v", got, b.Hot())
	}
}

// TestLocalReject verifies the three distinct local errors and no-trace failure.
func TestLocalReject(t *testing.T) {
	cases := []struct {
		name string
		bad  []Event
		want error
	}{
		{"empty", nil, ErrEmptyBatch},
		{"empty key", []Event{{"A", 1}, {"", 1}}, ErrEmptyKey},
		{"zero delta", []Event{{"A", 1}, {"B", 0}}, ErrZeroDelta},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewBuffer(2)
			if _, err := b.Feed([]Event{{"A", 1}, {"B", 1}}); err != nil {
				t.Fatal(err)
			}
			snapP, snapH, snapProbe := b.PendingAll(), b.Hot(), b.probe
			if _, err := b.Feed(c.bad); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			if !eqMap(b.PendingAll(), snapP) || !eqStr(b.Hot(), snapH) || b.probe != snapProbe {
				t.Fatalf("rejected batch left a trace: %v %v %d", b.PendingAll(), b.Hot(), b.probe)
			}
		})
	}
}

// TestProbeDirectMap proves a threshold trigger inspects an m-independent key
// count: plain buffering never scans, and the trigger after a direct map hit
// records exactly one inspected key at every scale m.
func TestProbeDirectMap(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		b := NewBuffer(3)
		evs := make([]Event, 0, m)
		for i := 0; i < m; i++ {
			evs = append(evs, Event{Key: fmt.Sprintf("k%05d", i), Delta: 1})
		}
		if _, err := b.Feed(evs); err != nil || b.probe != 0 {
			t.Fatalf("m=%d: plain buffer must not inspect keys, probe=%d", m, b.probe)
		}
		push, err := b.Feed([]Event{{Key: "k00000", Delta: 1}, {Key: "k00000", Delta: 1}})
		if err != nil || !eqEvents(push, []Event{{"k00000", 3}}) || b.probe > 1 {
			t.Fatalf("m=%d: trigger inspected %d keys (must be O(1) constant)", m, b.probe)
		}
	}
}

func eqEvents(a, b []Event) bool {
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

func eqStr(a, b []string) bool {
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

func eqMap(a, b map[string]int64) bool {
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
