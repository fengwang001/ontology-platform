package diff_test

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"

	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
)

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func roundTrip(t *testing.T, src, tgt []byte, bs int) *diff.Patch {
	t.Helper()
	s, err := sig.Build(tgt, bs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := diff.Diff(src, s)
	out, err := patch.Apply(tgt, p.Encode())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("result != src (len %d vs %d)", len(out), len(src))
	}
	return p
}

// 六种源/目标关系：新数据字节数与复用块数必须满足最小化约束。
func TestRelations(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const bs = 16
	base := randBytes(rng, 8*bs)
	mid := bytes.Clone(base)
	rng.Read(mid[3*bs : 4*bs]) // 只改中间一块
	other := randBytes(rng, 8*bs)
	cases := []struct {
		name      string
		src, tgt  []byte
		maxNew    int // -1 表示不检查上界
		exactNew  int // -1 表示不检查精确值
		wantReuse int
	}{
		{"完全相同", base, base, -1, 0, 8},
		{"中间差一块", mid, base, 2 * bs, bs, 7},
		{"目标为前缀", append(bytes.Clone(base), randBytes(rng, 10)...), base, -1, 10, 8},
		{"源为前缀", base[:5*bs], base, -1, 0, 5},
		{"完全不同", other, base, -1, 8 * bs, 0},
		{"源为空", nil, base, -1, 0, 0},
	}
	for _, tc := range cases {
		p := roundTrip(t, tc.src, tc.tgt, bs)
		if got := p.NewDataBytes(); tc.exactNew >= 0 && got != tc.exactNew {
			t.Errorf("%s: newData=%d want %d", tc.name, got, tc.exactNew)
		} else if tc.maxNew >= 0 && got > tc.maxNew {
			t.Errorf("%s: newData=%d > %d", tc.name, got, tc.maxNew)
		}
		if got := p.ReusedBlocks(); got != tc.wantReuse {
			t.Errorf("%s: reused=%d want %d", tc.name, got, tc.wantReuse)
		}
		if p.Stats.StrongComputed > p.Stats.WeakMatches {
			t.Errorf("%s: strong=%d > weakMatches=%d", tc.name, p.Stats.StrongComputed, p.Stats.WeakMatches)
		}
	}
}

// 块大小大于数据长度时整块作为唯一不满块处理。
func TestBlockLargerThanData(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	data := randBytes(rng, 10)
	p := roundTrip(t, data, data, 64)
	if p.ReusedBlocks() != 1 || p.NewDataBytes() != 0 {
		t.Errorf("reused=%d new=%d", p.ReusedBlocks(), p.NewDataBytes())
	}
}

// 弱校验和碰撞必须被强校验和挡住：目标块 [1,0,0,1] 与源块 [0,1,1,0]
// 弱值相同，diff 不得复用目标块，应用结果仍与源端一致。
func TestCollisionBlocked(t *testing.T) {
	cases := []struct {
		tgt, src  []byte
		wantReuse int
		wantNew   int
	}{
		{[]byte{1, 0, 0, 1}, []byte{0, 1, 1, 0}, 0, 4},
		{[]byte{1, 0, 0, 1, 7, 7, 7, 7}, []byte{0, 1, 1, 0, 7, 7, 7, 7}, 1, 4},
	}
	for _, tc := range cases {
		p := roundTrip(t, tc.src, tc.tgt, 4)
		if p.ReusedBlocks() != tc.wantReuse || p.NewDataBytes() != tc.wantNew {
			t.Errorf("reused=%d new=%d, want reuse=%d new=%d",
				p.ReusedBlocks(), p.NewDataBytes(), tc.wantReuse, tc.wantNew)
		}
		if p.Stats.WeakMatches == 0 {
			t.Errorf("expected weak collision to be hit")
		}
		if p.Stats.StrongComputed > p.Stats.WeakMatches {
			t.Errorf("strong=%d > weakMatches=%d", p.Stats.StrongComputed, p.Stats.WeakMatches)
		}
	}
}

// 同一对（源、目标）生成补丁必须确定：20 次逐字节相同。
func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	tgt := randBytes(rng, 300)
	src := bytes.Clone(tgt)
	rng.Read(src[100:140])
	s, err := sig.Build(tgt, 16)
	if err != nil {
		t.Fatal(err)
	}
	first := diff.Diff(src, s).Encode()
	for i := 0; i < 19; i++ {
		if got := diff.Diff(src, s).Encode(); !bytes.Equal(got, first) {
			t.Fatalf("run %d: patch differs", i)
		}
	}
}

// 多协程并发对不同数据对做 diff 互不干扰（-race 必须干净）。
func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			tgt := randBytes(rng, 500)
			src := bytes.Clone(tgt)
			rng.Read(src[200:260])
			s, err := sig.Build(tgt, 32)
			if err != nil {
				t.Errorf("Build: %v", err)
				return
			}
			out, err := patch.Apply(tgt, diff.Diff(src, s).Encode())
			if err != nil || !bytes.Equal(out, src) {
				t.Errorf("seed %d: out=%v err=%v", seed, len(out), err)
			}
		}(int64(g))
	}
	wg.Wait()
}

// 签名编解码往返一致。
func TestSigRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, bs := range []int{1, 7, 64} {
		s, err := sig.Build(randBytes(rng, 100), bs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := sig.Decode(s.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if got.BlockSize != s.BlockSize || len(got.Blocks) != len(s.Blocks) {
			t.Fatalf("bs=%d: header mismatch", bs)
		}
		for i := range s.Blocks {
			if got.Blocks[i] != s.Blocks[i] {
				t.Fatalf("bs=%d block %d mismatch", bs, i)
			}
		}
	}
}
