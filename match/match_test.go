package match

import (
	"math/rand"
	"testing"

	"ontology/window"
)

// drive 模拟编码器逐位置查询匹配器，返回考察的候选位置总数。
// lookahead 截断到 256 字节，保证单位置比较代价有界。
func drive(t *testing.T, data []byte, chain int) int {
	t.Helper()
	w, err := window.New(1 << 15)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(w, chain)
	if err != nil {
		t.Fatal(err)
	}
	src := func(q uint64) byte { return data[q] }
	for pos := 0; pos+MinMatch <= len(data); pos++ {
		end := pos + 256
		if end > len(data) {
			end = len(data)
		}
		m.FindLongest(src, uint64(pos), data[pos:end])
		w.Append(data[pos])
	}
	return m.Examined()
}

func TestExaminedBound(t *testing.T) {
	const chain = 16
	rng := rand.New(rand.NewSource(1))
	random := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	cases := []struct {
		name string
		data []byte
	}{
		{"same-64KB", make([]byte, 64<<10)},
		{"same-4MB", make([]byte, 4<<20)},
		{"random-64KB", random(64 << 10)},
		{"random-4MB", random(4 << 20)},
	}
	perByte := map[string]float64{}
	for _, c := range cases {
		got := drive(t, c.data, chain)
		if limit := chain * len(c.data); got > limit {
			t.Errorf("%s: examined %d > chain*n %d", c.name, got, limit)
		}
		perByte[c.name] = float64(got) / float64(len(c.data))
		t.Logf("%s: examined=%d per-byte=%.4f", c.name, got, perByte[c.name])
	}
	small, big := perByte["same-64KB"], perByte["same-4MB"]
	if small > 2*big || big > 2*small {
		t.Errorf("per-byte examined grows with scale: 64KB=%.4f 4MB=%.4f", small, big)
	}
}

func TestNewRejectsBadChain(t *testing.T) {
	w, _ := window.New(8)
	if _, err := New(w, 0); err != ErrZeroChain {
		t.Fatalf("chain=0: got %v", err)
	}
}
