package phase

import "testing"

// 推进判定是 O(1)：最后一次 Arrive 检查的 party 数恒为 1，不随 m 增长。
func TestArriveChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		s, err := New(m)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for i := 0; i < m-1; i++ {
			if _, err := s.Arrive(i); err != nil {
				t.Fatalf("m=%d arrive %d: %v", m, i, err)
			}
		}
		if _, err := s.Arrive(m - 1); err != nil {
			t.Fatalf("m=%d last arrive: %v", m, err)
		}
		if s.checked != 1 {
			t.Fatalf("m=%d: last Arrive checked %d parties, want 1", m, s.checked)
		}
		if s.Phase() != 1 {
			t.Fatalf("m=%d: phase=%d, want 1", m, s.Phase())
		}
	}
}

func TestNewRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if _, err := New(n); err != ErrNoParties {
			t.Fatalf("n=%d: err=%v, want ErrNoParties", n, err)
		}
	}
}
