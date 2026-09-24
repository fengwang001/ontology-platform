package api_test

import (
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

// shuffled 生成 m 个互异、带空洞的 LSN（i*3+7），按 LCG 伪随机顺序到达。
func shuffled(m int, seed uint64) []int64 {
	out := make([]int64, m)
	for i := range out {
		out[i] = int64(i*3 + 7)
	}
	for i := m - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(seed>>33) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// batchRef 批量参照：升序排序，并在 [min,max] 内取缺失整数。
func batchRef(vals []int64) (sorted, holes []int64) {
	sorted = slices.Clone(vals)
	slices.Sort(sorted)
	if len(sorted) == 0 {
		return nil, nil
	}
	for i, want := 0, sorted[0]; ; want++ {
		if i < len(sorted) && sorted[i] == want {
			i++
		} else {
			holes = append(holes, want)
		}
		if want == sorted[len(sorted)-1] {
			return sorted, holes
		}
	}
}

func feed(t *testing.T, vals []int64) *api.Log {
	l := api.New()
	for _, v := range vals {
		if err := l.Append(v); err != nil {
			t.Fatalf("append %d: %v", v, err)
		}
	}
	return l
}

// 不变量1：任意 Append 序列后，重编号与空洞都与批量重算逐值相同。
func TestBatchEquivalence(t *testing.T) {
	for _, m := range []int{1, 2, 7, 100, 1000} {
		vals := shuffled(m, uint64(m))
		l := feed(t, vals)
		sorted, holes := batchRef(vals)
		if got := l.Holes(); !slices.Equal(got, holes) {
			t.Fatalf("m=%d holes got %v want %v", m, got, holes)
		}
		for new, old := range sorted {
			if got, err := l.FindNew(old); err != nil || got != int64(new) {
				t.Fatalf("m=%d FindNew(%d)=%d,%v want %d", m, old, got, err, new)
			}
		}
	}
}

// 不变量2：互逆可寻址，两个方向逐值核验。
func TestInverse(t *testing.T) {
	l := feed(t, shuffled(500, 3))
	for k := 0; k < l.Count(); k++ {
		old, e1 := l.FindOld(int64(k))
		back, e2 := l.FindNew(old)
		again, e3 := l.FindOld(back)
		if e1 != nil || e2 != nil || e3 != nil || back != int64(k) || again != old {
			t.Fatalf("k=%d old=%d back=%d again=%d err=%v,%v,%v", k, old, back, again, e1, e2, e3)
		}
	}
}

// 不变量3：空洞守恒 len(Holes)==(max-min+1)-n 且 >=0；无空洞时为空（含于守恒式）。
func TestHoleConservation(t *testing.T) {
	cases := [][]int64{nil, {42}, {5, 6, 7, 8}, {10, 13, 15, 12, 17, 20, 18}, {0, 2, 4}}
	for ci, vals := range cases {
		l := feed(t, vals)
		holes, n, span := l.Holes(), l.Count(), 0
		if n > 0 {
			span = int(slices.Max(vals)-slices.Min(vals)) + 1
		}
		if len(holes) != span-n || len(holes) < 0 {
			t.Fatalf("case%d: len(holes)=%d span=%d n=%d", ci, len(holes), span, n)
		}
	}
}

// 不变量4：失败不留痕——四类可判定错误互不相同，拒绝后状态不变。
func TestFailureNoTrace(t *testing.T) {
	l := feed(t, []int64{10, 13, 15})
	n0, h0 := l.Count(), l.Holes()
	ops := []func() error{
		func() error { return l.Append(-1) },
		func() error { return l.Append(10) },
		func() error { _, e := l.FindNew(11); return e },
		func() error { _, e := l.FindOld(-1); return e },
		func() error { _, e := l.FindOld(3); return e },
	}
	wants := []error{api.ErrNegative, api.ErrDuplicate, api.ErrUnknown, api.ErrOutOfRange, api.ErrOutOfRange}
	for i, op := range ops {
		if err := op(); err != wants[i] {
			t.Fatalf("case%d: got %v want %v", i, err, wants[i])
		}
		if l.Count() != n0 || !slices.Equal(l.Holes(), h0) {
			t.Fatalf("case%d: state changed after reject", i)
		}
	}
}

// 并发：N 个 goroutine 只读同一实例，Holes 与 FindNew 结果逐值相同。
func TestConcurrentReads(t *testing.T) {
	l := feed(t, shuffled(300, 7))
	h0 := l.Holes()
	var wg sync.WaitGroup
	bad, res := make([]bool, 16), make([][]int64, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				old, e1 := l.FindOld(int64(k % l.Count()))
				nw, e2 := l.FindNew(old)
				if e1 != nil || e2 != nil || !slices.Equal(l.Holes(), h0) || l.SelfCheck() != nil {
					bad[i] = true
				}
				res[i] = append(res[i], nw)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 16; i++ {
		if bad[i] || !slices.Equal(res[i], res[0]) {
			t.Fatalf("goroutine %d 读到了不一致的结果", i)
		}
	}
}
