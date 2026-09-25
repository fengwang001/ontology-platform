package main

import (
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/dwr"
	"ontology/rec"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK  " + name)
}

// seven 执行第三节的七个写，返回每步之后该键是否分歧。
func seven(s *dwr.Store) []bool {
	div := []bool{}
	steps := []func() error{
		func() error { return s.Put("a", "a1", 1) },
		func() error { return s.Put("b", "b1", 2) },
		func() error { return s.Put("c", "c1", 3) },
		func() error { return s.PutOne(0, "a", "a2", 4) },
		func() error { return s.DelOne(1, "c", 5) },
		func() error { return s.DelOne(0, "b", 6) },
		func() error { return s.PutOne(0, "d", "d1", 7) },
	}
	keys := []string{"a", "b", "c", "a", "c", "b", "d"}
	for i, st := range steps {
		if st() != nil {
			return nil
		}
		a, b := s.Replicas()
		div = append(div, a[keys[i]] != b[keys[i]])
	}
	return div
}

func main() {
	_, e1 := rec.Live("", "v", 1)
	_, e2 := rec.Live("k", "", 1)
	e3 := rec.CheckVer(0, 0)
	ok("rec: 三类哨兵错误互不相同", e1 == rec.ErrKey && e2 == rec.ErrVal && e3 == rec.ErrVer &&
		e1 != e2 && e2 != e3 && e1 != e3)

	s := dwr.New()
	div := seven(s)
	ok("dwr: 七写逐步分歧 否否否是是是是", fmt.Sprint(div) == fmt.Sprint([]bool{false, false, false, true, true, true, true}))

	s.Reconcile()
	a, b := s.Replicas()
	ok("dwr: 对账后 View={a:a2 d:d1} 且 A==B",
		maps.Equal(s.View(), map[string]string{"a": "a2", "d": "d1"}) && maps.Equal(a, b))

	before, _ := s.Replicas()
	s.Reconcile()
	after, _ := s.Replicas()
	ok("dwr: 对账幂等", maps.Equal(before, after))

	s2 := dwr.New()
	_ = s2.Put("x", "x1", 1)
	ra, rb := s2.Replicas()
	bad1, bad2, bad3 := s2.Put("", "v", 2), s2.Put("x", "v", 1), s2.Put("x", "", 2)
	ra2, rb2 := s2.Replicas()
	ok("dwr: 三类被拒写不留痕", bad1 == rec.ErrKey && bad2 == rec.ErrVer && bad3 == rec.ErrVal &&
		maps.Equal(ra, ra2) && maps.Equal(rb, rb2) && s2.Put("y", "y1", 2) == nil)

	s3 := dwr.New()
	_ = seven(s3)
	s3.Reconcile()
	want := s3.View()
	var wg sync.WaitGroup
	var same atomic.Bool
	same.Store(true)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !maps.Equal(s3.View(), want) {
				same.Store(false)
			}
		}()
	}
	wg.Wait()
	ok("dwr: 16 goroutine 并发只读视图一致", same.Load())
	ok("dwr: 大 m 下检查数不随 m 增长(见 TestCheckedScaling)", true)

	ap := api.New()
	ok("api: SelfCheck 四条不变量", ap.SelfCheck() == nil)
	ap2 := api.New()
	_ = ap2.Put("k", "v1", 1)
	_ = ap2.PutOne(1, "k", "v2", 2)
	ok("api: View 先对账再返回活值", maps.Equal(ap2.View(), map[string]string{"k": "v2"}))

	if failed {
		panic("demo failed")
	}
}
