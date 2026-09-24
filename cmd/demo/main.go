package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/asof"
	"ontology/interval"
	"ontology/store"
)

var passed, failed int

func judge(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	iv, err := interval.New(10, 20)
	judge("interval: 起点命中终点不命中", err == nil && iv.Contains(10) && !iv.Contains(20))
	_, err = interval.New(7, 7)
	judge("interval: 空区间被拒", errors.Is(err, interval.ErrEmpty))

	s := store.New()
	salary, _ := interval.New(2020, 2022)
	if _, err := s.Write("salary", 100, salary); err != nil {
		judge("store: 删除后历史可见而当前不存在", false)
		return
	}
	txD, err := s.Delete("salary", 2021)
	before, after := visibleAt(s, "salary", 2020, txD), visibleAt(s, "salary", 2021, txD)
	judge("store: 删除后历史可见而当前不存在", before == 100 && after < 0)

	judge("store: 随机一千次操作后矩形不相交", randomOpsDisjoint())
	judge("store: 并发更正后不变量仍成立", concurrentDisjoint())
	judgeSalary()
	judgeCheckedBound()
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		panic("demo checks failed")
	}
}

// visibleAt 返回 (有效时刻, 事务时刻) 下可见的值，不存在返回 -1。
func visibleAt(s *store.Store, key string, validAt, txAt int64) int64 {
	for _, r := range s.Snapshot(key) {
		if r.VisibleAt(validAt, txAt) {
			return r.Value
		}
	}
	return -1
}

func randomOpsDisjoint() bool {
	s := store.New()
	rng := rand.New(rand.NewSource(1))
	keys := []string{"a", "b", "c", "d"}
	for i := 0; i < 1000; i++ {
		key := keys[rng.Intn(len(keys))]
		start := int64(rng.Intn(100))
		iv := interval.I{Start: start, End: start + 1 + int64(rng.Intn(10))}
		switch rng.Intn(3) {
		case 0:
			_, _ = s.Write(key, int64(i), iv)
		case 1:
			_, _ = s.Correct(key, int64(i), iv)
		default:
			_, _ = s.Delete(key, start+1)
		}
	}
	return s.CheckDisjoint() == nil
}

func concurrentDisjoint() bool {
	s := store.New()
	v, err := interval.New(2020, 2021)
	if err != nil {
		return false
	}
	if _, err := s.Write("hot", 0, v); err != nil {
		return false
	}
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 100; i++ {
				_, _ = s.Correct("hot", base+i, v)
			}
		}(int64(g) * 100)
	}
	wg.Wait()
	return s.CheckDisjoint() == nil
}

func judgeSalary() {
	s := store.New()
	v, _ := interval.New(2020, 2021)
	t1, _ := s.Write("salary", 100, v)
	t2, _ := s.Correct("salary", 120, v)
	q := asof.New(s)
	atT1, f1 := q.Query("salary", 2020, t1)
	atT2, f2 := q.Query("salary", 2020, t2)
	judge("asof: 同一有效时刻两个事务时刻不同值", f1 && f2 && atT1 == 100 && atT2 == 120)

	zero, _ := interval.New(2020, 2021)
	tz, _ := s.Write("zero", 0, zero)
	zv, zf := q.Query("zero", 2020, tz)
	_, nf := q.Query("ghost", 2020, tz)
	judge("asof: 不存在与零值可区分", zf && zv == 0 && !nf)

	_, early := q.Query("salary", 2020, t1-1)
	judge("asof: 事务时刻过早返回不存在", !early)
}

func judgeCheckedBound() {
	s := store.New()
	v, _ := interval.New(2020, 2021)
	for k := 0; k < 1000; k++ {
		key := fmt.Sprintf("key-%d", k)
		if _, err := s.Write(key, 0, v); err != nil {
			judge("asof: 点查检查数", false)
			return
		}
		for i := 1; i < 10; i++ {
			_, _ = s.Correct(key, int64(i), v)
		}
	}
	q := asof.New(s)
	q.Query("key-500", 2020, 1<<60)
	checked, bound := q.Checked(), int64(40)
	judge(fmt.Sprintf("asof: 点查检查数 %d<=%d", checked, bound), checked <= bound)
}
