package exp

import (
	"testing"

	"ontology/wlog"
)

// TestCheckedConstant pins the complexity bound: one Next inspects a
// constant number of log entries regardless of log size m.
func TestCheckedConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		l := wlog.New()
		for i := 0; i < m; i++ {
			if _, err := l.Write("k", i); err != nil {
				t.Fatal(err)
			}
		}
		s := NewSession(l, nil)
		if err := s.Start(m); err != nil { // S = m
			t.Fatal(err)
		}
		if _, ok, err := s.Next(); err != nil || !ok {
			t.Fatalf("m=%d: Next failed", m)
		}
		if s.checked > 2 {
			t.Fatalf("m=%d: checked %d entries, want <= constant", m, s.checked)
		}
	}
}
