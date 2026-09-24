package slide_test

import (
	"slices"
	"testing"

	"ontology/slide"
)

// naive 是仅供测试对照的朴素双循环实现，不进产出包。
func naive(seq []int, w int) []int {
	out := make([]int, 0, len(seq)-w+1)
	for j := 0; j+w <= len(seq); j++ {
		m := seq[j]
		for _, v := range seq[j+1 : j+w] {
			m = max(m, v)
		}
		out = append(out, m)
	}
	return out
}

type shapeCase struct {
	name string
	seq  []int
}

func shapes(n int) []shapeCase {
	eq, inc, dec := make([]int, n), make([]int, n), make([]int, n)
	for i := range eq {
		eq[i], inc[i], dec[i] = 7, i, n-i
	}
	return []shapeCase{{"全相等", eq}, {"单调递增", inc}, {"单调递减", dec}}
}

func TestMaxesMatchesNaive(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		for _, sh := range shapes(n) {
			got, err := slide.Maxes(sh.seq, 17)
			if err != nil || !slices.Equal(got, naive(sh.seq, 17)) {
				t.Errorf("%s n=%d 与朴素实现不一致", sh.name, n)
			}
		}
	}
}

func TestThreeThreeTwo(t *testing.T) {
	got, err := slide.Maxes([]int{3, 3, 2}, 2)
	if err != nil || !slices.Equal(got, []int{3, 3}) {
		t.Fatalf("Maxes=%v err=%v", got, err)
	}
	s, _ := slide.New(2)
	for i, v := range []int{3, 3, 2} {
		if m := s.Feed(v); m != 3 || s.SelfCheck() != nil {
			t.Fatalf("第 %d 步 max=%d", i, m)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for _, sh := range shapes(500) {
		s, _ := slide.New(9)
		for _, v := range sh.seq {
			if s.Feed(v); s.SelfCheck() != nil {
				t.Fatalf("%s: %v", sh.name, s.SelfCheck())
			}
		}
	}
}
