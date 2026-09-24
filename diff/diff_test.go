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
)

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

// roundtrip 走完整链路：签名 → 编码/解码 → diff → 补丁编码 → 应用。
func roundtrip(t *testing.T, src, target []byte, bs int) diff.Stats {
	t.Helper()
	sg, err := sig.Generate(target, bs)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sg, err = sig.Decode(sg.Encode())
	if err != nil {
		t.Fatalf("sig Decode: %v", err)
	}
	p, st, err := diff.Diff(src, sg)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	out, err := patch.Apply(target, p.Encode())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("result != src (len %d vs %d)", len(out), len(src))
	}
	return st
}

func TestDiffBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	full := randBytes(rng, 640) // 10 个 64 字节块
	cases := []struct {
		name       string
		src, tgt   []byte
		bs         int
		maxNew     int
		wantErr    error
	}{
		{"both empty", nil, nil, 64, 0, nil},
		{"src empty", nil, full, 64, 0, nil},
		{"target empty", full, nil, 64, 640, nil},
		{"identical", full, full, 64, 0, nil},
		{"target is prefix", append(append([]byte(nil), full...), randBytes(rng, 320)...), full, 64, 320, nil},
		{"src is prefix", full[:320], full, 64, 0, nil},
		{"bs exceeds length", full[:50], full[:50], 64, 50, nil},
		{"bs zero", full, full, 0, 0, chunk.ErrBlockSize},
		{"exact multiple", full, full, 64, 0, nil},
		{"tail partial", append(append([]byte(nil), full...), 1, 2, 3), full, 64, 3, nil},
		{"disjoint", randBytes(rng, 640), full, 64, 640, nil},
	}
	for _, c := range cases {
		_, err := sig.Generate(c.tgt, c.bs)
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("%s: Generate err=%v want %v", c.name, err, c.wantErr)
		}
		if c.wantErr != nil {
			continue
		}
		if st := roundtrip(t, c.src, c.tgt, c.bs); st.NewBytes > c.maxNew {
			t.Fatalf("%s: new bytes %d > %d", c.name, st.NewBytes, c.maxNew)
		}
	}
}

func TestDiffMinimalAndDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const bs = 64
	target := randBytes(rng, 100*bs)
	src := append([]byte(nil), target...)
	rng.Read(src[50*bs : 51*bs]) // 中间改一块
	sg, _ := sig.Generate(target, bs)
	var first []byte
	for i := 0; i < 20; i++ {
		p, st, err := diff.Diff(src, sg)
		if err != nil {
			t.Fatal(err)
		}
		if st.NewBytes > 2*bs {
			t.Fatalf("new bytes %d exceed 2*bs", st.NewBytes)
		}
		raw := p.Encode()
		if i == 0 {
			first = raw
		} else if !bytes.Equal(raw, first) {
			t.Fatal("patch not deterministic")
		}
	}
	_, stSame, _ := diff.Diff(target, sg)
	if stSame.NewBytes != 0 {
		t.Fatalf("identical: new bytes %d != 0", stSame.NewBytes)
	}
}

func TestWeakCollisionBlocked(t *testing.T) {
	const bs = 8
	a := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	b := []byte{2, 1, 3, 4, 5, 6, 7, 8} // 弱校验和相同、内容不同
	rng := rand.New(rand.NewSource(13))
	target := append(append([]byte(nil), b...), randBytes(rng, 5*bs)...)
	src := append([]byte(nil), a...)
	src = append(src, target[bs:]...)
	sg, err := sig.Generate(target, bs)
	if err != nil {
		t.Fatal(err)
	}
	chunk.ResetCounters()
	p, st, err := diff.Diff(src, sg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := patch.Apply(target, p.Encode())
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if st.ReuseBlocks != 5 { // 第 0 块必须不能被错误复用
		t.Fatalf("reuse %d, want 5 (colliding block must not be reused)", st.ReuseBlocks)
	}
	if chunk.Strongs() == 0 || chunk.Strongs() > int64(st.WeakHits) {
		t.Fatalf("strongs %d, weak hits %d", chunk.Strongs(), st.WeakHits)
	}
}

func TestWeakOpsBoundInDiff(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	const bs = 64
	target := randBytes(rng, 200*bs)
	src := append([]byte(nil), target...)
	rng.Read(src[100*bs : 101*bs])
	sg, _ := sig.Generate(target, bs)
	chunk.ResetCounters()
	if _, _, err := diff.Diff(src, sg); err != nil {
		t.Fatal(err)
	}
	if got, limit := chunk.Ops(), int64(4*len(src)); got > limit {
		t.Fatalf("weak ops %d exceed %d", got, limit)
	}
}

func TestDiffConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			target := randBytes(rng, 3000)
			src := append([]byte(nil), target...)
			rng.Read(src[1000:1064])
			sg, err := sig.Generate(target, 64)
			if err != nil {
				t.Error(err)
				return
			}
			p, _, err := diff.Diff(src, sg)
			if err != nil {
				t.Error(err)
				return
			}
			out, err := patch.Apply(target, p.Encode())
			if err != nil || !bytes.Equal(out, src) {
				t.Errorf("goroutine %d: roundtrip failed: %v", seed, err)
			}
		}(int64(g))
	}
	wg.Wait()
}
