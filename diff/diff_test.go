package diff

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"ontology/chunk"
	"ontology/sig"
)

const bs = 8

func mkdata(pattern string, blocks int) []byte {
	out := make([]byte, 0, blocks*bs)
	for b := 0; b < blocks; b++ {
		for j := 0; j < bs; j++ {
			out = append(out, pattern[b%len(pattern)]+byte(j))
		}
	}
	return out
}

// rebuild 按补丁+目标数据重建结果。
func rebuild(p *Patch, target []byte) []byte {
	var out []byte
	blocks, _ := chunk.Split(target, p.BlockSize)
	for _, in := range p.Instrs {
		if in.Op == OpRef {
			out = append(out, blocks[in.Block].Data...)
		} else {
			out = append(out, in.Data...)
		}
	}
	return out
}

func TestDiffScenarios(t *testing.T) {
	base := mkdata("ABCDEFGH", 8)
	mid := append([]byte(nil), base...)
	for j := 0; j < bs; j++ {
		mid[3*bs+j] = byte(0xFF - j)
	}
	prefixTarget := base[:5*bs+3]
	prefixSource := base
	srcPrefixTarget := base
	srcPrefixSource := base[:5*bs+3]
	different := mkdata("ZYXWVUTS", 8)

	cases := []struct {
		name     string
		source   []byte
		target   []byte
		wantRefs int
		maxLit   int
	}{
		{"identical", base, base, 8, 0},
		{"middle one block", mid, base, 7, 2 * bs},
		{"target is prefix", prefixSource, prefixTarget, 5, 3 * bs},
		{"source is prefix", srcPrefixSource, srcPrefixTarget, 5, 3},
		{"totally different", different, base, 0, len(different)},
		{"empty source", nil, base, 0, 0},
		{"both empty", nil, nil, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := sig.Generate(c.target, bs)
			if err != nil {
				t.Fatal(err)
			}
			p, stats := Build(c.source, s)
			if p.RefCount() != c.wantRefs {
				t.Fatalf("refs=%d want %d", p.RefCount(), c.wantRefs)
			}
			if p.LiteralBytes() > c.maxLit {
				t.Fatalf("literal=%d > %d", p.LiteralBytes(), c.maxLit)
			}
			if stats.StrongCount > stats.WeakHits {
				t.Fatalf("strong=%d > weakHits=%d", stats.StrongCount, stats.WeakHits)
			}
			if got := rebuild(p, c.target); !bytes.Equal(got, c.source) {
				t.Fatalf("rebuild mismatch")
			}
			if stats.WeakOps > 4*len(c.source) {
				t.Fatalf("weakOps=%d > 4*%d", stats.WeakOps, len(c.source))
			}
		})
	}
}

func TestCollisionIsNotReused(t *testing.T) {
	blockA := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	blockB := []byte{0, 2, 3, 4, 5, 6, 7, 9}
	if chunk.Weak(blockA) != chunk.Weak(blockB) {
		t.Fatal("setup: weak values must collide")
	}
	target := blockA
	source := blockB
	s, _ := sig.Generate(target, bs)
	p, _ := Build(source, s)
	if p.RefCount() != 0 {
		t.Fatalf("colliding block must not be reused, refs=%d", p.RefCount())
	}
	if got := rebuild(p, target); !bytes.Equal(got, source) {
		t.Fatal("result must equal source")
	}
}

func TestDeterministic(t *testing.T) {
	target := mkdata("ABCDEFGH", 6)
	source := append([]byte(nil), target...)
	source[2*bs+1] = 7
	s, _ := sig.Generate(target, bs)
	var first []byte
	for i := 0; i < 20; i++ {
		p, _ := Build(source, s)
		cur := fmt.Sprintf("%v", p.Instrs)
		if first == nil {
			first = []byte(cur)
		} else if string(first) != cur {
			t.Fatalf("run %d nondeterministic", i)
		}
	}
}

func TestConcurrent(t *testing.T) {
	done := make(chan error, 16)
	for k := 0; k < 16; k++ {
		k := k
		go func() {
			target := mkdata(string([]byte{byte('A' + k), 'B', 'C', 'D', 'E', 'F', 'G', 'H'}), 5)
			source := append([]byte(nil), target...)
			source[bs] ^= 0xFF
			s, err := sig.Generate(target, bs)
			if err != nil {
				done <- err
				return
			}
			p, _ := Build(source, s)
			if !bytes.Equal(rebuild(p, target), source) {
				done <- fmt.Errorf("goroutine %d mismatch", k)
				return
			}
			h := sha256.Sum256([]byte(fmt.Sprintf("%v", p.Instrs)))
			done <- nil
			_ = h
		}()
	}
	for k := 0; k < 16; k++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
