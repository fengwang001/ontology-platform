package diff_test

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

// roundTrip 走完整链路：签名→diff→编码→解码→应用→最终校验，断言结果与源一致。
func roundTrip(t *testing.T, src, tgt []byte, bs int) diff.Patch {
	t.Helper()
	sg, err := sig.Generate(tgt, bs)
	if err != nil {
		t.Fatal(err)
	}
	p := diff.Diff(src, sg)
	dec, err := patch.Decode(patch.Encode(p))
	if err != nil {
		t.Fatalf("解码补丁: %v", err)
	}
	out, err := patch.Apply(tgt, dec)
	if err != nil {
		t.Fatalf("应用补丁: %v", err)
	}
	if err := verify.Final(out, dec); err != nil {
		t.Fatalf("最终校验: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("结果与源不一致")
	}
	return p
}

// 六种源/目标关系下的补丁构成（数字为实测，见 FINDINGS.md）。
func TestRelations(t *testing.T) {
	const bs = 8
	base := make([]byte, 40)
	rand.New(rand.NewSource(99)).Read(base)
	mid := append([]byte(nil), base...)
	for k := 2 * bs; k < 3*bs; k++ {
		mid[k] ^= 0xA5
	}
	other := make([]byte, 40)
	rand.New(rand.NewSource(7)).Read(other)
	cases := []struct {
		name            string
		src, tgt        []byte
		newBytes, reuse int
	}{
		{"相同", base, base, 0, 5},
		{"中间差一块", mid, base, 8, 4},
		{"目标为源的前缀", base, base[:24], 16, 3},
		{"源为目标的前缀", base[:24], base, 0, 3},
		{"完全不同", other, base, 40, 0},
		{"源为空", nil, base, 0, 0},
		{"目标为空", base, nil, 40, 0},
		{"两者皆空", nil, nil, 0, 0},
	}
	for _, c := range cases {
		p := roundTrip(t, c.src, c.tgt, bs)
		if p.NewBytes() != c.newBytes || p.ReuseCount() != c.reuse {
			t.Fatalf("%s：新数据 %d 复用 %d，期望 %d/%d",
				c.name, p.NewBytes(), p.ReuseCount(), c.newBytes, c.reuse)
		}
		if c.name == "中间差一块" && p.NewBytes() > 2*bs {
			t.Fatalf("中间差一块：新数据 %d 超过 2 倍块大小", p.NewBytes())
		}
	}
}

// 边界语义：块大于数据、整数倍、非整数倍、末块不满、单字节、块大小为 0。
func TestBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	data := func(n int) []byte { b := make([]byte, n); rng.Read(b); return b }
	cases := []struct {
		name     string
		src, tgt []byte
		bs       int
	}{
		{"块大于数据", data(10), data(10), 100},
		{"整数倍", data(64), data(64), 16},
		{"非整数倍", data(50), data(50), 16},
		{"末块不满", data(37), data(40), 16},
		{"单字节块", data(9), data(9), 1},
		{"源空目标空", nil, nil, 16},
	}
	for _, c := range cases {
		roundTrip(t, c.src, c.tgt, c.bs)
	}
	if _, err := sig.Generate(data(8), 0); !errors.Is(err, sig.ErrZeroBlockSize) {
		t.Fatalf("块大小为 0 应返回 ErrZeroBlockSize，得到 %v", err)
	}
}

// 核心验证：弱校验和碰撞必须被强校验和挡住，最终结果仍与源一致。
func TestWeakCollisionBlocked(t *testing.T) {
	tgt := []byte{4, 3, 2, 1, 9, 9, 9, 9}
	src := []byte{1, 2, 3, 4, 9, 9, 9, 9}
	if chunk.WeakSum(src[:4]) != chunk.WeakSum(tgt[:4]) {
		t.Fatal("前提不成立：两块应弱碰撞")
	}
	sg, _ := sig.Generate(tgt, 4)
	p := diff.Diff(src, sg)
	if p.WeakHits == 0 {
		t.Fatal("应发生过弱命中")
	}
	for _, in := range p.Instrs {
		if in.Reuse && in.Index == 0 {
			t.Fatal("碰撞块被错误复用")
		}
	}
	out, err := patch.Apply(tgt, p)
	if err != nil || verify.Final(out, p) != nil || !bytes.Equal(out, src) {
		t.Fatalf("碰撞场景结果不正确: %v", err)
	}
}

// 强校验只在弱匹配时计算：强校验次数 ≤ 弱匹配次数。
func TestStrongOnlyOnWeakHit(t *testing.T) {
	base := make([]byte, 200)
	rand.New(rand.NewSource(13)).Read(base)
	sg, _ := sig.Generate(base, 16)
	chunk.ResetCounters()
	p := diff.Diff(base, sg)
	if ops := chunk.StrongOps(); ops > int64(p.WeakHits) {
		t.Fatalf("强校验 %d 次超过弱匹配 %d 次", ops, p.WeakHits)
	}
}

// 同一（源，目标）对的补丁必须确定：20 次逐字节相同。
func TestDeterministic(t *testing.T) {
	src := make([]byte, 300)
	rand.New(rand.NewSource(17)).Read(src)
	tgt := make([]byte, 300)
	rand.New(rand.NewSource(19)).Read(tgt)
	sg, _ := sig.Generate(tgt, 16)
	want := patch.Encode(diff.Diff(src, sg))
	for i := 0; i < 20; i++ {
		if got := patch.Encode(diff.Diff(src, sg)); !bytes.Equal(got, want) {
			t.Fatalf("第 %d 次生成的补丁不一致", i)
		}
	}
}

// 多协程并发对不同数据对做 diff，互不干扰（-race 必须干净）。
func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			src := make([]byte, 500)
			rng.Read(src)
			tgt := make([]byte, 500)
			rng.Read(tgt)
			sg, err := sig.Generate(tgt, 16)
			if err != nil {
				t.Error(err)
				return
			}
			p := diff.Diff(src, sg)
			out, err := patch.Apply(tgt, p)
			if err != nil || verify.Final(out, p) != nil || !bytes.Equal(out, src) {
				t.Errorf("协程 %d 往返失败: %v", seed, err)
			}
		}(int64(g))
	}
	wg.Wait()
}
