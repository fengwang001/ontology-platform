package sketch

import (
	"fmt"
	"testing"
)

func TestSketch(t *testing.T) {
	t.Run("内存与条目数无关", func(t *testing.T) {
		s := New(4096)
		want := s.Bytes()
		for _, n := range []int{1000, 10000, 100000} {
			for i := 0; i < n; i++ {
				s.Increment(fmt.Sprintf("key-%d", i))
			}
			if got := s.Bytes(); got != want {
				t.Fatalf("插入 %d 键后 Bytes=%d, 期望不变 %d", n, got, want)
			}
		}
	})

	cases := []struct {
		name  string
		incr  int
		halve int
		want  int64
	}{
		{"计数", 8, 0, 8},
		{"减半一次", 8, 1, 4},
		{"减半三次", 100, 3, 12},
		{"减半至零", 1, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(4096)
			for i := 0; i < tc.incr; i++ {
				s.Increment("hot")
			}
			for i := 0; i < tc.halve; i++ {
				s.Halve()
			}
			if got := s.Estimate("hot"); got != tc.want {
				t.Fatalf("Estimate=%d, 期望 %d", got, tc.want)
			}
		})
	}
}
