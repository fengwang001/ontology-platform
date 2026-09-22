// 区间重叠索引演示：go run ./cmd/demo，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"

	"ontology/ival"
	"ontology/query"
	"ontology/tree"
)

func iv(l, r int64) ival.Interval { return ival.Interval{L: l, R: r} }

var failed bool

func report(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	// 1. 相接不重叠，点查 3 只属于右区间。
	tr := tree.New()
	tr.Insert(iv(1, 3))
	tr.Insert(iv(3, 5))
	e := query.New(tr)
	r1, _ := e.Overlap(iv(1, 3))
	r2, _ := e.Overlap(iv(3, 5))
	abuttingOK := len(r1) == 1 && len(r2) == 1 &&
		reflect.DeepEqual(e.Stab(3), []ival.Interval{iv(3, 5)})
	report("相接不重叠且点查3归属右区间", abuttingOK)

	// 2. 零长度：可插入、不被点查、不被重叠查询。
	z := tree.New()
	z.Insert(iv(2, 2))
	z.Insert(iv(1, 5))
	ze := query.New(z)
	zo, _ := ze.Overlap(iv(1, 5))
	report("零长度可插入且任何查询不返回", z.Len() == 2 && len(ze.Stab(2)) == 1 && len(zo) == 1)

	// 3. 固定种子随机多轮，与朴素扫描一致。4. 插入顺序无关。
	report("随机300轮与朴素扫描完全一致", randomNaive(300))
	data := []ival.Interval{iv(2, 6), iv(1, 3), iv(2, 6), iv(4, 9), iv(1, 3)}
	a := query.New(build(data, nil))
	b := query.New(build(data, []int{4, 3, 2, 1, 0}))
	aa, _ := a.Overlap(iv(0, 10))
	bb, _ := b.Overlap(iv(0, 10))
	report("结果顺序与插入顺序无关", reflect.DeepEqual(aa, bb))

	// 5. 多重集删除一次只删一个。
	m := tree.New()
	for k := 0; k < 3; k++ {
		m.Insert(iv(2, 4))
	}
	me := query.New(m)
	m.Delete(iv(2, 4))
	mr, _ := me.Overlap(iv(0, 10))
	report("多重集删除一次只删一个", len(mr) == 2 && m.Len() == 2)

	// 6. 删除后不再命中。
	m.Delete(iv(2, 4))
	m.Delete(iv(2, 4))
	mr, _ = me.Overlap(iv(0, 10))
	report("全部删除后不再命中", len(mr) == 0)
	// 7. 三类可判定错误；8. 拒绝后仍可用。
	bad := tree.New(tree.WithMaxIntervals(1))
	bad.Insert(iv(0, 1))
	errBad := bad.Insert(iv(3, 2))
	errNF := bad.Delete(iv(8, 9))
	errLim := bad.Insert(iv(2, 3))
	_, errBatch := query.New(bad, query.WithMaxBatch(1)).BatchOverlap([]ival.Interval{iv(0, 1), iv(0, 1)})
	usable := bad.SelfCheck() == nil
	errInsAfter := bad.Delete(iv(0, 1))
	errIns2 := bad.Insert(iv(4, 5))
	report("三类错误可判定且拒绝后仍可用",
		errors.Is(errBad, ival.ErrInvalidInterval) && errors.Is(errNF, tree.ErrNotFound) &&
			errors.Is(errLim, tree.ErrLimitExceeded) && errors.Is(errBatch, query.ErrBatchLimit) &&
			usable && errInsAfter == nil && errIns2 == nil)

	// 9. 并发查询逐位一致。
	report("32 goroutine 并发查询逐位一致", concurrentConsistent())
	// 10. 两档规模访问节点数对照（命中一个的点查）。
	report(fmt.Sprintf("N=1000 点查访问%d节点(<=100)", visits(1000)), visits(1000) <= 100)
	v := visits(100_000)
	report(fmt.Sprintf("N=100000 点查访问%d节点(<=100)", v), v <= 100)
	if failed {
		fmt.Println("FAIL overall")
	}
}

func build(data []ival.Interval, order []int) *tree.Tree {
	t := tree.New(tree.WithMaxIntervals(0))
	if order == nil {
		for _, x := range data {
			t.Insert(x)
		}
		return t
	}
	for _, i := range order {
		t.Insert(data[i])
	}
	return t
}

func randomNaive(rounds int) bool {
	rng := rand.New(rand.NewSource(20260923))
	t := tree.New()
	var all []ival.Interval
	for k := 0; k < rounds; k++ {
		l := rng.Int63n(40)
		x := iv(l, l+rng.Int63n(8))
		if rng.Intn(3) == 0 && len(all) > 0 {
			v := all[rng.Intn(len(all))]
			if err := t.Delete(v); err != nil {
				return false
			}
			for i := range all {
				if all[i] == v {
					all = append(all[:i], all[i+1:]...)
					break
				}
			}
		} else if err := t.Insert(x); err == nil {
			all = append(all, x)
		}
		ql := rng.Int63n(45)
		q := iv(ql, ql+rng.Int63n(10))
		got, err := query.New(t).Overlap(q)
		if err != nil {
			return false
		}
		var want []ival.Interval
		for _, x := range all {
			if x.Overlaps(q) {
				want = append(want, x)
			}
		}
		sort.SliceStable(want, func(i, j int) bool {
			return want[i].L < want[j].L || want[i].L == want[j].L && want[i].R < want[j].R
		})
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
	}
	return t.SelfCheck() == nil
}

func concurrentConsistent() bool {
	t := tree.New(tree.WithMaxIntervals(0))
	for k := 0; k < 3000; k++ {
		t.Insert(iv(int64(2*k), int64(2*k+1)))
	}
	e := query.New(t)
	ref := e.Stab(5001)
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if !reflect.DeepEqual(e.Stab(5001), ref) {
					mu.Lock()
					ok = false
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	return ok
}

func visits(n int) uint64 {
	t := tree.New(tree.WithMaxIntervals(n + 1))
	for k := 0; k < n; k++ {
		t.Insert(iv(int64(2*k), int64(2*k+1)))
	}
	var v uint64
	got := t.Stab(int64(2*(n-1)), func() { v++ })
	if len(got) != 1 {
		return ^uint64(0)
	}
	return v
}
