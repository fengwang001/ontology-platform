package resv

import (
	"fmt"
	"math/bits"
	"testing"

	"ontology/rnd"
)

// TestVisitBounds 池已满（k=m）时：键低于池底者访问数不超过小常数；
// 键高于池顶者不超过 2·⌈log2 m⌉+4（堆顶直接比较+下沉，不整池扫描）。
func TestVisitBounds(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		us := make([]float64, m+2)
		for i := range us {
			us[i] = 0.5
		}
		us[m], us[m+1] = 0.4, 0.9
		src, err := rnd.New(us)
		if err != nil {
			t.Fatal(err)
		}
		p := NewPool(m)
		for i := 0; i < m; i++ {
			if _, err := p.Consider(src, fmt.Sprintf("e%d", i), 1); err != nil {
				t.Fatal(err)
			}
		}
		in, err := p.Consider(src, "low", 1) // 键 0.4 < 池底 0.5，未入选
		if err != nil || in {
			t.Fatalf("m=%d low: in=%v err=%v", m, in, err)
		}
		if p.visits > 2 {
			t.Errorf("m=%d low: visits=%d, want <= 2", m, p.visits)
		}
		in, err = p.Consider(src, "high", 1) // 键 0.9 > 池顶 0.5，替换堆顶
		if err != nil || !in {
			t.Fatalf("m=%d high: in=%v err=%v", m, in, err)
		}
		if bound := 2*bits.Len(uint(m-1)) + 4; p.visits > bound {
			t.Errorf("m=%d high: visits=%d, want <= %d", m, p.visits, bound)
		}
	}
}
