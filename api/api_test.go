package api_test

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/api"
)

type ev struct{ part, pos, val int64 }

func genEvents(P, m int) [][]ev {
	per := make([][]ev, P)
	for p := range per {
		for i := 0; i < m; i++ {
			per[p] = append(per[p], ev{int64(p), int64(i), int64((i*7+p*13)%97 + 1)})
		}
	}
	return per
}

// interleave 保持分区内位点序、跨分区按 seed 随机交错。
func interleave(per [][]ev, seed uint64) []ev {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
	idx, left := make([]int, len(per)), 0
	for _, l := range per {
		left += len(l)
	}
	var out []ev
	for left > 0 {
		if p := rng.IntN(len(per)); idx[p] < len(per[p]) {
			out = append(out, per[p][idx[p]])
			idx[p]++
			left--
		}
	}
	return out
}

// naive 朴素参照：按分区收集（位点序即到达序），w=min(各分区条数)。
func naive(fed []ev, P int) (w int, total, prefix int64) {
	per := make([][]int64, P)
	for _, e := range fed {
		per[e.part] = append(per[e.part], e.val)
		total += e.val
	}
	w = len(per[0])
	for _, v := range per[1:] {
		w = min(w, len(v))
	}
	for _, v := range per {
		for i := 0; i < w; i++ {
			prefix += v[i]
		}
	}
	return
}

func feed(t *testing.T, a *api.Router, seq []ev) {
	t.Helper()
	for i, e := range seq {
		if err := a.Append(int(e.part), e.pos, e.val); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

// TestNaiveConsistency 不变量1：任意交错后 Total/PrefixTotal 等于朴素参照。
func TestNaiveConsistency(t *testing.T) {
	for _, c := range []struct{ P, m, cut int }{{2, 9, 6}, {3, 50, 100}, {8, 200, 1000}, {5, 1000, 2500}} {
		seq := interleave(genEvents(c.P, c.m), uint64(c.P*c.m+1))
		for _, k := range []int{c.cut, len(seq)} {
			a := api.New(c.P)
			feed(t, a, seq[:k])
			w, total, prefix := naive(seq[:k], c.P)
			if a.W() != w || a.Total() != total || a.PrefixTotal() != prefix {
				t.Fatalf("P=%d m=%d k=%d: got %d/%d/%d, want %d/%d/%d",
					c.P, c.m, k, a.W(), a.Total(), a.PrefixTotal(), w, total, prefix)
			}
		}
	}
}

// TestMonotonicity 不变量2：W/PrefixTotal/Total 逐步非递减。
func TestMonotonicity(t *testing.T) {
	for _, c := range []struct{ P, m int }{{2, 9}, {4, 300}} {
		a := api.New(c.P)
		var w0, p0, t0 int64
		for i, e := range interleave(genEvents(c.P, c.m), uint64(c.m+3)) {
			if err := a.Append(int(e.part), e.pos, e.val); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
			w, p, tt := int64(a.W()), a.PrefixTotal(), a.Total()
			if w < w0 || p < p0 || tt < t0 {
				t.Fatalf("step %d: (%d,%d,%d) < (%d,%d,%d)", i, w, p, tt, w0, p0, t0)
			}
			w0, p0, t0 = w, p, tt
		}
	}
}

// TestInterleaveEquivalence 不变量3：同一批事件任意交错，终值相同。
func TestInterleaveEquivalence(t *testing.T) {
	per := genEvents(4, 120)
	var w0, p0, t0 int64
	for seed := uint64(1); seed <= 6; seed++ {
		a := api.New(4)
		feed(t, a, interleave(per, seed))
		if seed == 1 {
			w0, p0, t0 = int64(a.W()), a.PrefixTotal(), a.Total()
		} else if int64(a.W()) != w0 || a.PrefixTotal() != p0 || a.Total() != t0 {
			t.Fatalf("seed %d: final stats differ", seed)
		}
	}
}

// TestFailureAtomicity 不变量4：三类错误互不相同、拒绝零改动、之后仍可用。
func TestFailureAtomicity(t *testing.T) {
	a := api.New(3)
	feed(t, a, interleave(genEvents(3, 5), 42))
	w0, p0, t0 := a.W(), a.PrefixTotal(), a.Total()
	bads := []struct {
		part, pos, val int64
		want           error
	}{{3, 0, 1, api.ErrPartOutOfRange}, {-1, 0, 1, api.ErrPartOutOfRange},
		{0, 99, 1, api.ErrGap}, {1, 5, -2, api.ErrNegative}}
	for i, b := range bads {
		if err := a.Append(int(b.part), b.pos, b.val); !errors.Is(err, b.want) {
			t.Fatalf("bad[%d]=%v, want %v", i, err, b.want)
		}
	}
	if a.W() != w0 || a.PrefixTotal() != p0 || a.Total() != t0 {
		t.Fatal("rejected ops mutated state")
	}
	if err := a.Append(0, 5, 10); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

// TestConcurrentReads 第六节：N 个 goroutine 并发只读，逐字段相同。
func TestConcurrentReads(t *testing.T) {
	const P, m, N = 4, 50, 16
	a := api.New(P)
	feed(t, a, interleave(genEvents(P, m), 9))
	w0, p0, t0 := a.W(), a.PrefixTotal(), a.Total()
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				ok := a.W() == w0 && a.PrefixTotal() == p0 && a.Total() == t0
				for p := 0; p < P; p++ {
					ok = ok && a.PartCount(p) == m && a.PartSum(p) > 0
				}
				if !ok {
					t.Error("concurrent read mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
