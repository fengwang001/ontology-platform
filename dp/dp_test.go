package dp

import (
	"math/rand"
	"testing"
)

// fullTable 填完整 O(n·m) 表求最长公共子串，作为滚动行的对照。
func fullTable(a, b string) (best, start int) {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				d[i][j] = d[i-1][j-1] + 1
			}
			if d[i][j] > best {
				best, start = d[i][j], i-d[i][j]
			}
		}
	}
	return best, start
}

func randStr(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(4)) // 小字母表，公共子串命中多
	}
	return string(b)
}

func TestRollingMatchesFullTable(t *testing.T) {
	cases := [][2]string{
		{"banana", "ananas"}, {"abcx", "abc"}, {"abcde", "abfce"},
		{"aabbaabb", "bbaabbaa"}, {"xyz", "abc"}, {"aaaa", "aa"},
		{"mississippi", "issip"}, {"abracadabra", "cadabra"}, {"a", "a"},
	}
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 2, 5, 17, 50} {
		for _, m := range []int{1, 3, 8, 33, 80} {
			cases = append(cases, [2]string{randStr(rng, n), randStr(rng, m)})
		}
	}
	for _, p := range cases {
		gl, gs := New(p[0]).Longest(p[1])
		fl, fs := fullTable(p[0], p[1])
		if gl != fl || gs != fs {
			t.Errorf("a=%q b=%q: rolling=(%d,%d) full=(%d,%d)", p[0], p[1], gl, gs, fl, fs)
		}
	}
}

func TestRetainedCellsConstant(t *testing.T) {
	const n = 100
	rng := rand.New(rand.NewSource(2))
	c := New(randStr(rng, n))
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c.Longest(randStr(rng, m))
		c.mu.Lock()
		got := c.cells
		c.mu.Unlock()
		if want := min(n, m) + 1; got != want {
			t.Errorf("m=%d: retained cells=%d, want %d (不随 m 增长)", m, got, want)
		}
	}
}
