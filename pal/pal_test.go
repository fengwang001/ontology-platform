package pal

import "testing"

// lcgString 生成确定性伪随机串，alphabet 越小回文越密集。
func lcgString(n int, alpha, seed uint32) string {
	b := make([]byte, n)
	for i := range b {
		seed = seed*1664525 + 1013904223
		b[i] = byte('a' + seed>>24%alpha)
	}
	return string(b)
}

// TestNodeCountLinear 断言节点总数不超过 n+2：不同回文子串数线性于 n，
// 而非「每个回文子串一个节点」的 O(n^2)。直接读非导出计数器 total。
func TestNodeCountLinear(t *testing.T) {
	cases := []struct {
		n     int
		alpha uint32
	}{
		{100, 2}, {100, 26}, {500, 3}, {1000, 2}, {1000, 26}, {5000, 5}, {10000, 2}, {10000, 26},
	}
	for _, c := range cases {
		for seed := uint32(1); seed <= 3; seed++ {
			s := lcgString(c.n, c.alpha, seed*7919)
			tr := Build(s)
			if tr.total != len(tr.nodes) {
				t.Fatalf("n=%d: total=%d != len(nodes)=%d", c.n, tr.total, len(tr.nodes))
			}
			if tr.total > c.n+2 {
				t.Fatalf("n=%d alpha=%d: total=%d > n+2=%d", c.n, c.alpha, tr.total, c.n+2)
			}
		}
	}
}

// TestLinkStructure 钉住 link 树结构：指向更短回文、严格递减故无环、
// len 为 1 的节点指向偶根。
func TestLinkStructure(t *testing.T) {
	strings := []string{"aabaa", "abba", "a", "aaaa", "abcba", lcgString(300, 2, 7), lcgString(300, 26, 9)}
	for _, s := range strings {
		tr := Build(s)
		if !tr.CheckLinks() {
			t.Fatalf("s=%q: CheckLinks failed", s)
		}
		for i := 2; i < len(tr.nodes); i++ {
			v, lk := &tr.nodes[i], &tr.nodes[tr.nodes[i].link]
			if lk.len >= v.len {
				t.Fatalf("s=%q node %d: len[link]=%d >= len=%d", s, i, lk.len, v.len)
			}
			if v.len == 1 && tr.nodes[i].link != 1 {
				t.Fatalf("s=%q node %d: len-1 node not linked to even root", s, i)
			}
			// 沿 link 走至多 len(nodes) 步必到根，否则有环。
			for cur, steps := i, 0; cur > 1; cur, steps = tr.nodes[cur].link, steps+1 {
				if steps > len(tr.nodes) {
					t.Fatalf("s=%q node %d: link cycle", s, i)
				}
			}
		}
	}
}
