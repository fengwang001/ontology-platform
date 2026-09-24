package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/cmp"
	"ontology/lsn"
)

func must(t *testing.T, seq []int64) *api.Engine {
	e := api.New()
	for _, v := range seq {
		if err := e.Append(v); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// randSeq 返回 n 个 [0,span) 互异 LSN 的随机排列（Perm 前 n 项即随机子集随机序）。
func randSeq(seed, n, span int) []int64 {
	r := rand.New(rand.NewSource(int64(seed)))
	s := make([]int64, n)
	for i, p := range r.Perm(span)[:n] {
		s[i] = int64(p)
	}
	return s
}

func failIf(t *testing.T, cond bool, args ...any) {
	if cond {
		t.Error(args...)
	}
}

// verifyAll 对拍批量 oracle（排序赋 0..n-1 + [min,max] 枚举缺失），钉住不变量 1/2/3。
func verifyAll(t *testing.T, seq []int64) *api.Engine {
	e := must(t, seq)
	so := append([]int64(nil), seq...)
	sort.Slice(so, func(i, j int) bool { return so[i] < so[j] })
	h := []int64{}
	for i := 1; i < len(so); i++ {
		for v := so[i-1] + 1; v < so[i]; v++ {
			h = append(h, v)
		}
	}
	failIf(t, !reflect.DeepEqual(e.Holes(), h), "holes", seq)
	for k, old := range so {
		nk, _ := e.FindNew(old)
		b, _ := e.FindOld(int64(k))
		failIf(t, nk != int64(k) || b != old, "renumber", k)
	}
	if len(so) >= 2 {
		w := so[len(so)-1] - so[0] + 1 - int64(len(so))
		failIf(t, int64(len(h)) != w || w < 0, "conservation", w)
	} else {
		failIf(t, len(h) != 0, "n<2 holes must be empty")
	}
	return e
}

func TestEightStepHoles(t *testing.T) {
	e := api.New()
	want := [][]int64{{}, {11, 12}, {11, 12, 14}, {11, 14}, {11, 14, 16}, {11, 14, 16}, {11, 14, 16, 18, 19}, {11, 14, 16, 19}}
	for i, v := range []int64{10, 13, 15, 12, 17, 10, 20, 18} {
		err := e.Append(v)
		failIf(t, (i == 5 && !errors.Is(err, lsn.ErrDuplicate)) || (i != 5 && err != nil), "step", i+1, err)
		failIf(t, !reflect.DeepEqual(e.Holes(), want[i]), "step", i+1, e.Holes())
	}
	n, _ := e.FindNew(15)
	o, _ := e.FindOld(0)
	failIf(t, n != 3 || o != 10, "FindNew(15)/FindOld(0)", n, o)
}

func TestBatchReferenceRandom(t *testing.T) {
	for ci, c := range []struct{ n, sp int }{{0, 50}, {1, 50}, {5, 20}, {50, 200}, {500, 5000}, {2000, 100000}} {
		verifyAll(t, randSeq(ci+1, c.n, c.sp))
	}
}

func TestInverseAddressing(t *testing.T) {
	for _, seq := range [][]int64{{0}, {10, 13}, {15, 10, 13, 12}, {10, 13, 15, 12, 17, 20, 18}} {
		verifyAll(t, seq)
	}
}

func TestHoleConservation(t *testing.T) {
	for _, seq := range [][]int64{{10}, {10, 11}, {10, 13}, {0, 5}, {10, 13, 15, 12, 17, 20, 18}} {
		verifyAll(t, seq)
	}
}

// TestRejectedOpsNoTrace 钉不变量 4：每步状态不变，末尾 verifyAll 复核，四类哨兵互异、仍可用。
func TestRejectedOpsNoTrace(t *testing.T) {
	e := must(t, []int64{10, 13, 15})
	fs := []func() error{
		func() error { return e.Append(-7) },
		func() error { return e.Append(13) },
		func() error { _, er := e.FindNew(12); return er },
		func() error { _, er := e.FindOld(-1); return er },
		func() error { _, er := e.FindOld(3); return er },
	}
	ws := []error{lsn.ErrNegative, lsn.ErrDuplicate, cmp.ErrUnknownLSN, cmp.ErrNewIndexOutOfRange, cmp.ErrNewIndexOutOfRange}
	seen := map[error]bool{}
	for i, f := range fs {
		failIf(t, !errors.Is(f(), ws[i]), "reject", i)
		seen[ws[i]] = true
		nk, _ := e.FindNew(13) // 同一引擎被拒后查询仍正确 → 不留痕且可继续使用
		failIf(t, e.Count() != 3 || nk != 1 || !reflect.DeepEqual(e.Holes(), []int64{11, 12, 14}), "state changed", i)
	}
	failIf(t, len(seen) != 4, "sentinel errors must be 4 distinct")
}

func TestConcurrentReadOnly(t *testing.T) {
	seq := randSeq(42, 1000, 10000)
	e := must(t, seq)
	refH, refN := e.Holes(), make([]int64, len(seq))
	for i, v := range seq {
		refN[i], _ = e.FindNew(v)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			failIf(t, !reflect.DeepEqual(e.Holes(), refH), "concurrent Holes differs")
			for i, v := range seq {
				nk, err := e.FindNew(v)
				failIf(t, err != nil || nk != refN[i], "concurrent FindNew differs", i)
			}
		}()
	}
	close(start)
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	e := api.New()
	failIf(t, e.SelfCheck() != nil, "SelfCheck failed")
	failIf(t, e.Count() != 0, "SelfCheck must not mutate the receiver")
}
