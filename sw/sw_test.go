package sw

import (
	"fmt"
	"testing"

	"ontology/snap"
)

// TestIncrementalIsConstant 钉住复杂度不变量：不同快照规模 m 下，
// 单次 ApplyIncremental 扫描的 state 条目数不随 m 线性增长（恒 <= 1）。
func TestIncrementalIsConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			table := make(map[string]int64, m)
			for i := 0; i < m; i++ {
				table[fmt.Sprintf("k%06d", i)] = int64(i)
			}
			s := New()
			if err := s.ApplySnapshot(snap.Snapshot{SP: 7, Table: table}); err != nil {
				t.Fatalf("ApplySnapshot: %v", err)
			}
			if err := s.ApplyIncremental(snap.Event{Pos: 8, Key: "k000000", Delta: 1}); err != nil {
				t.Fatalf("ApplyIncremental: %v", err)
			}
			if s.scanned > 1 {
				t.Fatalf("scanned=%d grows with m=%d, want O(1)", s.scanned, m)
			}
		})
	}
}

// TestRejectionsLeaveNoTrace 拒绝后 state 与 applied 全部不变，且可继续用。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(s *Switcher) error
	}{
		{"gap", func(s *Switcher) error { return s.ApplyIncremental(snap.Event{Pos: 9, Key: "x", Delta: 1}) }},
		{"negative SP", func(s *Switcher) error {
			return s.ApplySnapshot(snap.Snapshot{SP: -1, Table: map[string]int64{"z": 9}})
		}},
		{"empty key", func(s *Switcher) error { return s.ApplyIncremental(snap.Event{Pos: 3, Key: "", Delta: 1}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			if err := s.ApplySnapshot(snap.Snapshot{SP: 2, Table: map[string]int64{"a": 5}}); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if err := tc.op(s); err == nil {
				t.Fatal("want error, got nil")
			}
			if got := s.Applied(); got != 2 {
				t.Fatalf("applied=%d, want 2", got)
			}
			if got := s.View(); len(got) != 1 || got["a"] != 5 {
				t.Fatalf("state=%v, want {a:5}", got)
			}
			if err := s.ApplyIncremental(snap.Event{Pos: 3, Key: "a", Delta: 2}); err != nil {
				t.Fatalf("instance unusable after rejection: %v", err)
			}
		})
	}
}
