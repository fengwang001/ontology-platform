package pal

import (
	"math/rand"
	"testing"
)

func randStr(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(3))
	}
	return string(b)
}

// 节点总数（不同回文子串数+2）必须线性于 n，而非 O(n^2)。
func TestNodeCountLinear(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		tr := New(randStr(r, n))
		if tr.nodeTotal != len(tr.nodes) {
			t.Errorf("n=%d: nodeTotal=%d != len(nodes)=%d", n, tr.nodeTotal, len(tr.nodes))
		}
		if tr.nodeTotal > n+2 {
			t.Errorf("n=%d: nodeTotal=%d > n+2=%d", n, tr.nodeTotal, n+2)
		}
	}
}

// link 树结构：指向更短回文、无环、len==1 链偶根、link 目标是真后缀。
func TestLinkInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	cases := []string{"aabaa", "abba", "aaa", "abcba"}
	for i := 0; i < 5; i++ {
		cases = append(cases, randStr(r, 200))
	}
	for _, s := range cases {
		tr := New(s)
		for v := 2; v < len(tr.nodes); v++ {
			nd := tr.nodes[v]
			lk := tr.nodes[nd.link]
			if lk.length >= nd.length {
				t.Errorf("s=%q v=%d: len[link]=%d >= len=%d", s, v, lk.length, nd.length)
			}
			if nd.length == 1 && nd.link != 1 {
				t.Errorf("s=%q v=%d: len-1 node link=%d, want 1(偶根)", s, v, nd.link)
			}
			sub, lsub := tr.Sub(v), tr.Sub(nd.link)
			if len(lsub) > 0 && !hasSuffix(sub, lsub) {
				t.Errorf("s=%q: link(%q)=%q 不是真后缀", s, sub, lsub)
			}
			steps := 0 // 沿 link 走，length 严格递减故必在有限步内出非根区
			for u := v; u >= 2; u = tr.nodes[u].link {
				if steps++; steps > len(tr.nodes) {
					t.Fatalf("s=%q v=%d: link 成环", s, v)
				}
			}
		}
	}
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
