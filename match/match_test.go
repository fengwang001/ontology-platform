package match

import (
	"math/rand"
	"testing"

	"ontology/window"
)

func runParse(data []byte, chain int) int64 {
	win, _ := window.New(1 << 16)
	m, _ := New(win, chain)
	m.SetData(data)
	for p := 0; p < len(data); { // 与 enc.parse 相同的贪心推进
		_, n := m.Longest(p)
		if n == 0 {
			n = 1
		}
		for k := p; k < p+n; k++ {
			m.Advance(k)
		}
		p += n
	}
	return m.Candidates()
}

func TestComplexity(t *testing.T) {
	const chain = 128
	rnd := rand.New(rand.NewSource(1))
	rand64, rand4M := make([]byte, 64<<10), make([]byte, 4<<20)
	rnd.Read(rand64)
	rnd.Read(rand4M)
	cases := []struct {
		name string
		data []byte
	}{
		{"same64K", make([]byte, 64<<10)},
		{"same4M", make([]byte, 4<<20)},
		{"rand64K", rand64},
		{"rand4M", rand4M},
	}
	perByte := map[string]float64{}
	for _, c := range cases {
		got := runParse(c.data, chain)
		if max := int64(chain) * int64(len(c.data)); got > max {
			t.Errorf("%s: candidates %d > chain*len %d", c.name, got, max)
		}
		perByte[c.name] = float64(got) / float64(len(c.data))
		t.Logf("%s: candidates=%d perByte=%.4f", c.name, got, perByte[c.name])
	}
	a, b := perByte["same64K"], perByte["same4M"]
	if a > 2*b || b > 2*a {
		t.Errorf("per-byte candidates same64K=%g vs same4M=%g differ >2x", a, b)
	}
}

func TestLongest(t *testing.T) {
	cases := []struct {
		data         string
		pos, dist, n int
	}{
		{"aaaaaaaaaa", 1, 1, 9},   // 距离 1 重叠
		{"ababababab", 2, 2, 8},   // 距离 2 重叠
		{"abcabcabcabc", 3, 3, 9}, // 距离 3 重叠
		{"abcdXabcdY", 5, 5, 4},   // 普通匹配
		{"abcdefgh", 4, 0, 0},     // 无匹配
		{"xyz", 0, 0, 0},          // 不足 MinMatch
	}
	for _, c := range cases {
		win, _ := window.New(16)
		m, _ := New(win, 8)
		m.SetData([]byte(c.data))
		m.LoadRange(0, c.pos)
		dist, n := m.Longest(c.pos)
		if dist != c.dist || n != c.n {
			t.Errorf("%q pos %d: got (%d,%d), want (%d,%d)", c.data, c.pos, dist, n, c.dist, c.n)
		}
	}
}

func TestBadConfig(t *testing.T) {
	win, _ := window.New(16)
	if _, err := New(win, 0); err != ErrBadChain {
		t.Errorf("chain 0: got %v", err)
	}
	if _, err := window.New(0); err != window.ErrBadCap {
		t.Errorf("window 0: got %v", err)
	}
}
