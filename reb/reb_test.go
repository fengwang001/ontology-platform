package reb

import "testing"

// TestCheckedBound proves the per-op partition-check count does not grow
// with P: the same three steps have the same small bounds at every m.
func TestCheckedBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Join(m - 1); err != nil { // 此步不计
			t.Fatal(err)
		}
		steps := []struct {
			name string
			op   func() error
			max  int
		}{
			{"join5", func() error { return r.Join(5) }, 8},
			{"ackM1", func() error { return r.RevokeAck(m - 1) }, 16},
			{"leave5", func() error { return r.Leave(5) }, 16},
		}
		for _, s := range steps {
			if err := s.op(); err != nil {
				t.Fatalf("m=%d %s: %v", m, s.name, err)
			}
			if r.checked > s.max {
				t.Errorf("m=%d %s: checked=%d exceeds bound %d", m, s.name, r.checked, s.max)
			}
		}
	}
}
