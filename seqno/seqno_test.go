package seqno

import (
	"reflect"
	"testing"
)

func TestSetTable(t *testing.T) {
	tests := []struct {
		name      string
		observe   []int
		wantLCP   int
		wantMax   int
		wantHoles []int
		wantSteps int
	}{
		{"empty", nil, 0, 0, nil, 0},
		{"prefix", []int{1, 2, 3}, 3, 3, nil, 3},
		{"out of order 1,3", []int{1, 3}, 1, 3, []int{2}, 1},
		{"hole fills later", []int{1, 3, 2}, 3, 3, nil, 3},
		{"reverse 3,2,1", []int{3, 2, 1}, 3, 3, nil, 3},
		{"duplicates idempotent", []int{1, 1, 2, 2, 2}, 2, 2, nil, 2},
		{"gap then jump", []int{5}, 0, 5, []int{1, 2, 3, 4}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSet()
			seen := map[int]bool{}
			for _, seq := range tt.observe {
				got := s.Observe(seq)
				if want := !seen[seq]; got != want {
					t.Fatalf("Observe(%d)=%v want %v", seq, got, want)
				}
				seen[seq] = true
				if !s.Contains(seq) {
					t.Fatalf("Contains(%d)=false after Observe", seq)
				}
			}
			if s.LCP() != tt.wantLCP {
				t.Fatalf("LCP=%d want %d", s.LCP(), tt.wantLCP)
			}
			if s.Max() != tt.wantMax {
				t.Fatalf("Max=%d want %d", s.Max(), tt.wantMax)
			}
			got := s.Holes()
			if len(got) != len(tt.wantHoles) || (len(got) > 0 && !reflect.DeepEqual(got, tt.wantHoles)) {
				t.Fatalf("Holes=%v want %v", got, tt.wantHoles)
			}
			if s.Steps() != tt.wantSteps {
				t.Fatalf("Steps=%d want %d", s.Steps(), tt.wantSteps)
			}
			if got := s.Sorted(); len(got) != len(seen) {
				t.Fatalf("Sorted len=%d want %d (%v)", len(got), len(seen), got)
			}
		})
	}
}

func TestDiscardAbove(t *testing.T) {
	s := NewSet()
	for _, seq := range []int{1, 2, 3, 5} {
		s.Observe(seq)
	}
	if n := s.DiscardAbove(3); n != 1 {
		t.Fatalf("removed=%d want 1", n)
	}
	if s.LCP() != 3 || s.Max() != 3 {
		t.Fatalf("after discard lcp=%d max=%d want 3,3", s.LCP(), s.Max())
	}
	if s.Contains(5) {
		t.Fatal("seq 5 must be discarded")
	}

	s2 := NewSet()
	s2.Observe(1)
	s2.Observe(2)
	if n := s2.DiscardAbove(0); n != 2 || s2.LCP() != 0 || s2.Max() != 0 {
		t.Fatalf("rollback all: n=%d lcp=%d max=%d", n, s2.LCP(), s2.Max())
	}
}

// TestReverseAckLinearSteps 对照 100 与 10000 两档：倒序 Ack 下
// steps 恒等于首次 Observe 成功数（=N），证明与乱序程度无关。
func TestReverseAckLinearSteps(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := NewSet()
		firstSeen := 0
		for seq := n; seq >= 1; seq-- {
			if s.Observe(seq) {
				firstSeen++
			}
		}
		if s.LCP() != n {
			t.Fatalf("n=%d lcp=%d want %d", n, s.LCP(), n)
		}
		if s.Steps() != firstSeen || s.Steps() != n {
			t.Fatalf("n=%d steps=%d firstSeen=%d (naive rescan would be O(n^2))",
				n, s.Steps(), firstSeen)
		}
		t.Logf("reverse ack n=%d: steps=%d = firstObserve count; no prefix rescans", n, s.Steps())
	}
}
