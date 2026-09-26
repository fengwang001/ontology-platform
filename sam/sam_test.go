package sam

import (
	"math/rand"
	"testing"
)

func randBytes(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(4))
	}
	return string(b)
}

// 状态总数线性于 n：n+1 ≤ total ≤ 2n。同包测试直接读非导出计数器。
func TestStateCountLinear(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		a := Build(randBytes(r, n))
		if a.total < n+1 || a.total > 2*n {
			t.Fatalf("n=%d: total=%d 不在 [%d, %d]", n, a.total, n+1, 2*n)
		}
		if a.total != len(a.st) {
			t.Fatalf("n=%d: 计数器 %d != 实际状态数 %d", n, a.total, len(a.st))
		}
	}
}

// NOTES.md 第三节的八行表：s="abcbc" 的每个状态的 len 与 link。
func TestAbcbcTable(t *testing.T) {
	a := Build("abcbc")
	want := [][2]int{{0, -1}, {1, 0}, {2, 5}, {3, 7}, {4, 5}, {1, 0}, {5, 7}, {2, 0}}
	if a.total != len(want) {
		t.Fatalf("abcbc: total=%d, 应为 %d", a.total, len(want))
	}
	for v, w := range want {
		if a.st[v].len != w[0] || a.st[v].link != w[1] {
			t.Fatalf("状态 %d: 得到 (len=%d,link=%d), 应为 %v", v, a.st[v].len, a.st[v].link, w)
		}
	}
}

// link 树结构不变量：len[v]>len[link[v]]、无环、转移目标 len≥len[v]+1。
func TestLinkTreeInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	cases := []string{"abcbc", "aaaa", "ab", "z"}
	for i := 0; i < 20; i++ {
		cases = append(cases, randBytes(r, 1+r.Intn(50)))
	}
	for _, s := range cases {
		a := Build(s)
		for v := range a.st {
			if v == 0 {
				if a.st[v].link != -1 {
					t.Fatalf("s=%q: 根 link 应为 -1", s)
				}
				continue
			}
			p := a.st[v].link
			if p < 0 || p >= len(a.st) || a.st[v].len <= a.st[p].len {
				t.Fatalf("s=%q: 状态 %d 的 link 性质被破坏", s, v)
			}
			for b, to := range a.st[v].next {
				if a.st[to].len < a.st[v].len+1 {
					t.Fatalf("s=%q: 转移 %d -%c-> %d 的 len 不自洽", s, v, b, to)
				}
			}
			seen := map[int]bool{}
			for u := v; u != -1; u = a.st[u].link {
				if seen[u] {
					t.Fatalf("s=%q: 状态 %d 的 link 链有环", s, v)
				}
				seen[u] = true
			}
		}
	}
}
