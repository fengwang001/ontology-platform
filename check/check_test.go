package check

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"testing"

	"ontology/bloom"
)

const n, q = 10000, 10000

func val(i int) []byte { return []byte(fmt.Sprintf("v%d", i)) }

func filled(t *testing.T) *bloom.Filter {
	t.Helper()
	f, err := bloom.New(n, 0.01, 42)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		f.Add(val(i))
	}
	return f
}

func TestBadParam(t *testing.T) {
	cases := []struct {
		n uint64
		p float64
	}{{0, 0.01}, {10, 0}, {10, 1}, {10, -0.1}, {10, 1.1}}
	for _, c := range cases {
		if _, err := bloom.New(c.n, c.p, 1); !errors.Is(err, bloom.ErrBadParam) {
			t.Errorf("New(%d, %v) err=%v, want ErrBadParam", c.n, c.p, err)
		}
	}
}

func TestEmptyFilter(t *testing.T) {
	f := &bloom.Filter{}
	for _, i := range []int{0, 1, 999} {
		if f.MaybeContains(val(i)) {
			t.Errorf("empty filter claims v%d", i)
		}
	}
}

func TestBloom(t *testing.T) {
	f, g := filled(t), filled(t)
	m := uint64(math.Ceil(-n * math.Log(0.01) / (math.Ln2 * math.Ln2)))
	wantK := uint64(math.Round(float64(m) / n * math.Ln2))
	k1bits := make(map[uint64]bool) // 内联 k=1 错误实现：同样的 m，只用 1 个哈希
	h := fnv.New64a()
	k1 := func(b []byte) uint64 { h.Reset(); h.Write(b); return h.Sum64() % m }
	s := NewSet()
	for i := 0; i < n; i++ {
		k1bits[k1(val(i))] = true
		s.Add(val(i))
	}
	fpOpt, fp1, falseNeg, same := 0, 0, 0, true
	for i := 0; i < n+q; i++ {
		if i >= n && f.MaybeContains(val(i)) {
			fpOpt++
		}
		if s.Contains(val(i)) && !f.MaybeContains(val(i)) {
			falseNeg++
		}
		if i >= n && k1bits[k1(val(i))] {
			fp1++
		}
		if f.MaybeContains(val(i)) != g.MaybeContains(val(i)) {
			same = false
		}
	}
	got := make([]bool, q)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < q; i += 16 {
				got[i] = f.MaybeContains(val(n + i))
			}
		}(w)
	}
	wg.Wait()
	consistent := true
	for i := 0; i < q; i++ {
		if got[i] != f.MaybeContains(val(n+i)) {
			consistent = false
		}
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"no-false-negatives", falseNeg == 0},
		{"fp-rate-le-0.02", float64(fpOpt)/q <= 0.02},
		{"k1-worse-than-optimal-k", fp1 > fpOpt},
		{"query-reads-equals-k", f.Reads() == wantK && f.K() == wantK},
		{"deterministic-same-seed", same},
		{"concurrent-reads-consistent", consistent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("%s failed (fpOpt=%d fp1=%d k=%d)", c.name, fpOpt, fp1, wantK)
			}
		})
	}
}
