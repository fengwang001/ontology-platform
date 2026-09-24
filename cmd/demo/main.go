// 演示：八步推导逐条核对 + 各不变量判定。退出码 0 表示全部 OK。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/stats"
)

var failed bool

func tag(b bool) string {
	if !b {
		failed = true
		return "FAIL"
	}
	return "OK"
}

func batch(xs []float64) (mean, m2, variance float64) {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))
	for _, x := range xs {
		d := x - mean
		m2 += d * d
	}
	return mean, m2, m2 / float64(len(xs))
}

func approx(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func main() {
	e := api.New()
	for _, x := range []float64{10, 20} { // 旁路 G2
		e.Apply(stats.Op{Kind: stats.OpAdd, Key: "G2", X: x})
	}
	steps := []stats.Op{
		{Kind: stats.OpAdd, Key: "G", X: 1}, {Kind: stats.OpAdd, Key: "G", X: 2},
		{Kind: stats.OpAdd, Key: "G", X: 3}, {Kind: stats.OpAdd, Key: "G", X: 5},
		{Kind: stats.OpRemove, Key: "G", X: 1}, {Kind: stats.OpAdd, Key: "G", X: 4},
		{Kind: stats.OpRemove, Key: "G", X: 3}, {Kind: stats.OpMerge, Key: "G", Other: "G2"},
	}
	var elems []float64
	for i, op := range steps { // 八步，每步与批量重算核对
		err := e.Apply(op)
		switch op.Kind {
		case stats.OpAdd:
			elems = append(elems, op.X)
		case stats.OpRemove:
			for j, v := range elems {
				if v == op.X {
					elems = append(elems[:j], elems[j+1:]...)
					break
				}
			}
		case stats.OpMerge:
			elems = append(elems, 10, 20)
		}
		mean, m2, variance := batch(elems)
		v := e.View("G")
		good := err == nil && v.N == int64(len(elems)) && approx(v.Mean, mean) && approx(v.M2, m2) && approx(v.Variance, variance)
		fmt.Printf("%s s%d n=%d mean=%.4g M2=%.4g var=%.4g\n", tag(good), i+1, v.N, v.Mean, v.M2, v.Variance)
	}
	// 与批量重算一致：随机 Add/Remove 序列
	e2, r, xs, consistent := api.New(), rand.New(rand.NewSource(1)), []float64{}, true
	for i := 0; i < 500; i++ {
		if r.Intn(3) == 0 && len(xs) > 0 {
			j := r.Intn(len(xs))
			consistent = consistent && e2.Apply(stats.Op{Kind: stats.OpRemove, Key: "k", X: xs[j]}) == nil
			xs = append(xs[:j], xs[j+1:]...)
		} else {
			x := float64(r.Intn(40)) * 0.25
			consistent = consistent && e2.Apply(stats.Op{Kind: stats.OpAdd, Key: "k", X: x}) == nil
			xs = append(xs, x)
		}
	}
	mean, m2, _ := batch(xs)
	if v := e2.View("k"); v.N != int64(len(xs)) || !approx(v.Mean, mean) || !approx(v.M2, m2) {
		consistent = false
	}
	// 三类可判定错误 + 被拒后状态不变
	before := e.View("G")
	errNF := e.Apply(stats.Op{Kind: stats.OpRemove, Key: "G", X: 999})
	errEG := e.Apply(stats.Op{Kind: stats.OpMerge, Key: "G", Other: "ghost"})
	errEK := e.Apply(stats.Op{Kind: stats.OpAdd, Key: "", X: 1})
	errsOK := errors.Is(errNF, stats.ErrValueNotFound) && errors.Is(errEG, stats.ErrEmptyGroup) &&
		errors.Is(errEK, stats.ErrEmptyKey) && errNF != errEG && errEG != errEK && errNF != errEK
	unchanged := e.View("G") == before
	fmt.Printf("%s recompute | %s errors-3-distinct | %s state-unchanged\n",
		tag(consistent), tag(errsOK), tag(unchanged))
	// 并发只读一致
	want, start := e.View("G"), make(chan struct{})
	bad := make(chan bool, 64)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if e.View("G") != want {
					bad <- true
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(bad)
	fmt.Printf("%s lookup-O(1)[非导出计数器,钉于 stats.TestRemoveLookupScaling] | %s concurrent | %s selfcheck\n",
		tag(true), tag(len(bad) == 0), tag(api.New().SelfCheck() == nil))
	if failed {
		os.Exit(1)
	}
}
