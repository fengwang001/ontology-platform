package resv

import (
	"fmt"
	"testing"
)

// TestHeapVisitBound 钉死第四节复杂度：满池后
//   - 低于堆顶的新元素只做常数次比较（≤3，与 m 无关）；
//   - 高于堆顶的新元素至多 2⌈log2 m⌉+4 次比较（证明走堆顶比较而非整池扫描）。
//
// visits 为非导出字段，仅在此包内白盒读取；不经过任何导出方法外传数值。
func TestHeapVisitBound(t *testing.T) {
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		p := New(m)
		for i := 0; i < m; i++ { // W=1 时键=u，键落在 (0.1,0.9)
			u := 0.1 + 0.8*float64(i+1)/float64(m+1)
			p.Offer(fmt.Sprintf("f%d", i), 1, u)
		}
		if p.Len() != m {
			t.Fatalf("m=%d: pool not full: %d", m, p.Len())
		}

		p.Offer("lo", 1, 0.01) // 键低于池中最低者 -> 立即被堆顶拒绝
		if p.visits > 3 {
			t.Fatalf("m=%d: low-key element cost %d visits, want <= 3", m, p.visits)
		}
		if p.Len() != m {
			t.Fatalf("m=%d: rejected low-key element changed pool size", m)
		}

		p.Offer("hi", 1, 0.999) // 键高于池中最高者 -> 替换根并一路下沉
		bound := 2*ceilLog2(m) + 4
		if p.visits > bound {
			t.Fatalf("m=%d: high-key element cost %d visits, want <= %d", m, p.visits, bound)
		}
	}
}

// TestTieNeverReplaces 键相等时晚到者永不替换早到者（排名全序）。
func TestTieNeverReplaces(t *testing.T) {
	cases := []struct {
		name       string
		k          int
		id         string
		w, u       float64
		wantTopID  string
		wantAccept bool
	}{
		{"equal-key later loses, k=1", 1, "y", 2, 0.25, "x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New(c.k)
			p.Offer("x", 1, 0.5) // 键 .5
			got := p.Offer(c.id, c.w, c.u)
			if got != c.wantAccept {
				t.Fatalf("accept=%v want %v", got, c.wantAccept)
			}
			if es := p.Entries(); es[0].ID != c.wantTopID {
				t.Fatalf("top=%s want %s", es[0].ID, c.wantTopID)
			}
		})
	}
}
