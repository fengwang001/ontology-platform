package check_test

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"testing"

	"ontology/bits"
	"ontology/bloom"
	"ontology/check"
)

func val(p string, i int) []byte { return []byte(fmt.Sprintf("%s-%d", p, i)) }

func hash1(b []byte) uint64 { // 复刻 bloom 的 h1（seed=42），内联 k=1 对拍
	h := fnv.New64a()
	_, _ = h.Write(append([]byte{42, 0, 0, 0, 0, 0, 0, 0}, b...))
	return h.Sum64()
}

func TestBitSet(t *testing.T) {
	b := bits.New(130)
	for _, i := range []uint64{0, 63, 64, 129} {
		_ = b.Set(i)
	}
	idx := []uint64{0, 63, 64, 129, 1, 65, 130}
	for i, c := range idx {
		got, err := b.Get(c)
		if got != (i < 4) || (c == 130) != errors.Is(err, bits.ErrOutOfRange) {
			t.Fatalf("Get(%d)=%v,%v", c, got, err)
		}
	}
	if err := b.Set(200); !errors.Is(err, bits.ErrOutOfRange) || b.Len() != 130 {
		t.Fatal("Set/Len")
	}
}
func TestBadParamAndEmpty(t *testing.T) {
	ns := []uint64{0, 10, 10, 10, 10}
	ps := []float64{0.01, 0, 1, 1.5, -0.1}
	for i := range ns {
		if _, err := bloom.New(ns[i], ps[i], 1); !errors.Is(err, bloom.ErrBadParam) {
			t.Fatalf("New(%d,%v): %v", ns[i], ps[i], err)
		}
	}
	var f bloom.Filter // 零值：m、k 为 0
	f.Add([]byte("x")) // 不得 panic
	for _, p := range []string{"", "x", "anything"} {
		if f.MaybeContains([]byte(p)) {
			t.Fatalf("empty filter hit %q", p)
		}
	}
}
func TestNoFalseNegativeAndFPRate(t *testing.T) {
	f, _ := bloom.New(10000, 0.01, 42)
	s := check.NewSet()
	k1 := bits.New(f.M()) // 内联 k=1 错误实现：同 m、单哈希
	for i := 0; i < 10000; i++ {
		f.Add(val("v", i))
		s.Add(val("v", i))
		_ = k1.Set(hash1(val("v", i)) % f.M())
	}
	if err := check.Verify(f, s); err != nil { // 无假阴性
		t.Fatal(err)
	}
	fpOpt, fpK1 := 0, 0
	for i := 0; i < 10000; i++ {
		if f.MaybeContains(val("u", i)) {
			fpOpt++
		}
		if got, _ := k1.Get(hash1(val("u", i)) % f.M()); got {
			fpK1++
		}
	}
	if f.Reads() != f.K() || float64(fpOpt)/10000 > 0.02 || fpK1 <= fpOpt {
		t.Fatalf("k=%d reads=%d fp=%d, k=1 fp=%d", f.K(), f.Reads(), fpOpt, fpK1)
	}
}
func TestConcurrentReads(t *testing.T) {
	f, _ := bloom.New(1000, 0.01, 42)
	g, _ := bloom.New(1000, 0.01, 42)
	for i := 0; i < 1000; i++ {
		f.Add(val("v", i))
		g.Add(val("v", i))
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if f.MaybeContains(val("u", i)) != g.MaybeContains(val("u", i)) {
					t.Error("diverged")
				}
			}
		}()
	}
	wg.Wait()
}
