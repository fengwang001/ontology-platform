package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/part"
	"ontology/prune"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Printf("%s: FAIL\n", name)
		return
	}
	fmt.Printf("%s: OK\n", name)
}

var rows = []struct {
	id                 string
	lo, hi, minv, maxv int64
	static, dynamic    bool // 期望是否被裁
}{
	{"P0", 0, 10, 10, 20, true, false}, {"P1", 10, 20, 30, 40, true, false},
	{"P2", 20, 30, 50, 60, false, false}, {"P3", 30, 40, 15, 25, false, true},
	{"P4", 40, 50, 60, 80, false, true},
}

func build() *api.Pruner {
	pr := api.New()
	for _, r := range rows {
		if pr.AddPartition(r.id, r.lo, r.hi, r.minv, r.maxv) != nil {
			return nil
		}
	}
	return pr
}

// checkJudgments 第三节五个分区每个的静态/动态判定。
func checkJudgments() bool {
	for _, r := range rows {
		p, err := part.New(r.id, r.lo, r.hi, r.minv, r.maxv)
		if err != nil || p.StaticPrune(20, 50) != r.static ||
			(!r.static && p.DynamicPrune(30, 60) != r.dynamic) {
			return false
		}
	}
	return true
}

// 最终扫描集 + 不漏（被裁者边界盒与谓词至少一维无交集）。
func checkScanAndSound() bool {
	pr := build()
	scan, pruned, err := pr.Query(20, 50, 30, 60)
	if err != nil || !reflect.DeepEqual(scan, []string{"P2"}) ||
		!reflect.DeepEqual(pruned, []string{"P0", "P1", "P3", "P4"}) {
		return false
	}
	box := map[string][4]int64{}
	for _, r := range rows {
		box[r.id] = [4]int64{r.lo, r.hi, r.minv, r.maxv}
	}
	for _, id := range pruned {
		b := box[id]
		if !(b[1] <= 20 || b[0] >= 50) && !(b[3] < 30 || b[2] >= 60) {
			return false
		}
	}
	return true
}

// 静态/动态顺序无关：先动态后静态重算，扫描集相同。
func checkOrderFree() bool {
	scan, _, err := build().Query(20, 50, 30, 60)
	if err != nil {
		return false
	}
	scan2 := []string{}
	for _, r := range rows {
		p, _ := part.New(r.id, r.lo, r.hi, r.minv, r.maxv)
		if !p.DynamicPrune(30, 60) && !p.StaticPrune(20, 50) {
			scan2 = append(scan2, r.id)
		}
	}
	return reflect.DeepEqual(scan, scan2)
}

// 三类可判定错误互不相同 + 被拒后状态不变。
func checkErrorsAndState() bool {
	pr := build()
	before, _, _ := pr.Query(20, 50, 30, 60)
	e1 := pr.AddPartition("PX", 5, 5, 0, 1)
	_, _, e2 := pr.Query(50, 50, 30, 60)
	_, _, e3 := pr.QueryRefs([]string{"NOPE"}, 20, 50, 30, 60)
	if !errors.Is(e1, api.ErrInvalidPartition) || !errors.Is(e2, api.ErrInvalidPredicate) ||
		!errors.Is(e3, api.ErrUnknownPartition) || errors.Is(e1, e2) ||
		errors.Is(e2, e3) || errors.Is(e1, e3) {
		return false
	}
	after, _, err := pr.Query(20, 50, 30, 60)
	return err == nil && reflect.DeepEqual(before, after)
}

// checkConcurrent：64 个 goroutine 发同一查询，扫描集逐元素相同。
func checkConcurrent() bool {
	pr := build()
	want, _, _ := pr.Query(20, 50, 30, 60)
	var wg sync.WaitGroup
	res := make(chan []string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _, err := pr.Query(20, 50, 30, 60)
			if err != nil {
				s = nil
			}
			res <- s
		}()
	}
	wg.Wait()
	close(res)
	for s := range res {
		if !reflect.DeepEqual(s, want) {
			return false
		}
	}
	return true
}

func main() {
	report("P0..P4 static/dynamic judgments", checkJudgments())
	report("scan==[P2] && soundness(pruned bbox disjoint)", checkScanAndSound())
	report("static/dynamic order independence", checkOrderFree())
	report("3 distinct decidable errors && state unchanged", checkErrorsAndState())
	report("checked partitions sublinear in N", prune.CheckSublinear())
	report("concurrent queries identical", checkConcurrent())
	report("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
