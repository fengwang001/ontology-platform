package api_test

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sink"
	"ontology/wm"
)

func kv(p, o int) (string, int64) { return fmt.Sprintf("k%d", (p*7+o)%3), int64((p+1)*(o+3)) + 1 }
func chk(t *testing.T, bad bool, f string, a ...any) {
	t.Helper()
	if bad {
		t.Fatalf(f, a...)
	}
}

// gen 循环生成随机重投场景；片段并集恒为 [0,nx[p])，朴素参照对每个 (分区,位点) 各计一次。
func gen(P, L, steps int, seed uint64) ([][]wm.Rec, map[string]int64) {
	rng := rand.New(rand.NewPCG(seed, 99))
	nx := make([]int, P)
	var bs [][]wm.Rec
	for st := 0; st < steps; st++ {
		var b []wm.Rec // 空批也合法（提交后状态不变），可无条件收集
		for _, p := range rng.Perm(P)[:1+rng.IntN(P)] {
			if nx[p] >= L {
				continue
			}
			a := nx[p] - rng.IntN(nx[p]+1)
			e := a + 1 + rng.IntN(L-a)
			for o := a; o < e; o++ {
				k, v := kv(p, o)
				b = append(b, wm.Rec{Partition: p, Offset: int64(o), Key: k, Val: v})
			}
			nx[p] = max(nx[p], e)
		}
		bs = append(bs, b)
	}
	naive := map[string]int64{}
	for p := 0; p < P; p++ {
		for o := 0; o < nx[p]; o++ {
			k, v := kv(p, o)
			naive[k] += v
		}
	}
	return bs, naive
}
func writeAll(t *testing.T, s *api.Sink, bs [][]wm.Rec, ev int) {
	t.Helper()
	for i, b := range bs {
		if ev > 0 && i%ev == 0 {
			s.Restart()
		}
		chk(t, s.Write(b) != nil, "batch %d", i)
	}
}
func TestNaiveReferenceRandom(t *testing.T) { // 不变量1
	for _, c := range [][4]int{{2, 20, 80, 1}, {4, 60, 200, 290}, {3, 100, 300, 77}} {
		bs, naive := gen(c[0], c[1], c[2], uint64(c[3]))
		s := api.New(c[0])
		writeAll(t, s, bs, 0)
		chk(t, !maps.Equal(s.Table(), naive), "seed=%d %v != %v", c[3], s.Table(), naive)
	}
}
func TestPartitionIndependence(t *testing.T) { // 不变量2
	s := api.New(4)
	bs := [][]wm.Rec{
		{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 0, Offset: 1, Key: "a", Val: 2}},
		{{Partition: 2, Offset: 0, Key: "b", Val: 5}},
		{{Partition: 0, Offset: 1, Key: "a", Val: 2}},
		{{Partition: 2, Offset: 4, Key: "b", Val: 8}},
	}
	w2, dups := []int64{-1, 0, 0, 4}, []int64{0, 0, 1, 1}
	for i, b := range bs {
		chk(t, s.Write(b) != nil, "write")
		chk(t, s.Watermark(0) != 1 || s.Watermark(1) != -1 || s.Watermark(2) != w2[i] || s.Watermark(3) != -1, "marks %d", i)
		chk(t, s.Duplicates() != dups[i], "dups=%d want %d", s.Duplicates(), dups[i])
	}
}
func TestRestartInvariance(t *testing.T) { // 不变量3
	for _, c := range [][4]int{{2, 20, 80, 11}, {4, 60, 200, 290}} {
		bs, _ := gen(c[0], c[1], c[2], uint64(c[3]))
		base := api.New(c[0])
		writeAll(t, base, bs, 0)
		for _, ev := range []int{1, 3, 7} {
			s := api.New(c[0])
			writeAll(t, s, bs, ev)
			chk(t, !maps.Equal(s.Table(), base.Table()) || s.Duplicates() != base.Duplicates(), "seed=%d ev=%d diverged", c[3], ev)
			for p := 0; p < c[0]; p++ {
				chk(t, s.Watermark(p) != base.Watermark(p), "W[%d] diverged", p)
			}
		}
	}
}
func TestRejectedBatchLeavesNoTrace(t *testing.T) { // 不变量4
	s := api.New(2)
	chk(t, s.Write([]wm.Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1}}) != nil, "seed")
	badB := [][]wm.Rec{
		{{Partition: -1, Offset: -1, Key: "", Val: 1}, {Partition: 0, Offset: 0, Key: "a", Val: 1}},
		{{Partition: 0, Offset: 5, Key: "a", Val: 1}, {Partition: 0, Offset: 5, Key: "a", Val: 1}},
		{{Partition: 1, Offset: 0, Key: "b", Val: 1}, {Partition: 2, Offset: 0, Key: "c", Val: 1}},
	}
	badW := []error{wm.ErrIllegalRec, wm.ErrOutOfOrder, sink.ErrTooManyPartitions}
	for i, b := range badB {
		snap := s.Table()
		err := s.Write(b)
		chk(t, err != badW[i] || !maps.Equal(s.Table(), snap) || s.Watermark(1) != -1 || s.Watermark(2) != -1 || s.Duplicates() != 0, "case %d err=%v state=%v", i, err, s.Table())
	}
}
func TestSectionThreeWorkedExample(t *testing.T) { // 第三节：三批期望状态，批2 后 Restart
	s := api.New(8)
	bs := [][]wm.Rec{
		{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 0, Offset: 1, Key: "a", Val: 2}, {Partition: 1, Offset: 0, Key: "b", Val: 5}},
		{{Partition: 1, Offset: 1, Key: "b", Val: 3}, {Partition: 0, Offset: 1, Key: "a", Val: 2}, {Partition: 0, Offset: 3, Key: "a", Val: 4}},
		{{Partition: 1, Offset: 1, Key: "b", Val: 3}, {Partition: 0, Offset: 3, Key: "a", Val: 4}, {Partition: 1, Offset: 2, Key: "a", Val: 6}, {Partition: 0, Offset: 4, Key: "b", Val: 8}},
	}
	want := [][5]int64{{3, 5, 1, 0, 0}, {7, 8, 3, 1, 1}, {13, 16, 4, 2, 3}}
	for i, b := range bs {
		if i == 2 {
			s.Restart()
		}
		chk(t, s.Write(b) != nil, "batch %d", i)
		tb, w := s.Table(), want[i]
		chk(t, tb["a"] != w[0] || tb["b"] != w[1] || s.Watermark(0) != w[2] || s.Watermark(1) != w[3] || s.Duplicates() != w[4], "batch %d: %v W=[%d,%d] dups=%d", i+1, tb, s.Watermark(0), s.Watermark(1), s.Duplicates())
	}
}
func TestSelfCheck(t *testing.T) { chk(t, api.New(8).SelfCheck() != nil, "selfcheck") }
func TestConcurrentDuplicateWrite(t *testing.T) {
	for _, N := range []int{2, 8, 32} {
		b := []wm.Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 0, Offset: 1, Key: "a", Val: 2}, {Partition: 1, Offset: 0, Key: "b", Val: 5}, {Partition: 1, Offset: 1, Key: "b", Val: 3}}
		c := api.New(4)
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = c.Write(b) }()
		}
		wg.Wait()
		once := api.New(4)
		chk(t, once.Write(b) != nil, "once")
		chk(t, !maps.Equal(c.Table(), once.Table()) || c.Duplicates() != int64((N-1)*len(b)), "N=%d table=%v dups=%d", N, c.Table(), c.Duplicates())
	}
}
