// Command demo 对物化 join 索引做一次自检式演示，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/rel"
)

func pairs(s *api.System) string {
	var ps []string
	for _, a := range []int{1, 2} {
		for _, c := range s.Join(a) {
			ps = append(ps, fmt.Sprintf("(%d,%d)", a, c))
		}
	}
	return "[" + strings.Join(ps, " ") + "]"
}

func main() {
	failures := 0
	check := func(name string, ok bool, detail string) {
		status := "OK"
		if !ok {
			status, failures = "FAIL", failures+1
		}
		fmt.Println(status, name, detail)
	}

	s := api.New()
	steps := []struct {
		op   func() error
		size int
		want string
	}{
		{func() error { s.SetR(1, 10); return nil }, 0, "[]"},
		{func() error { s.SetR(2, 10); return nil }, 0, "[]"},
		{func() error { return s.AddS(10, 100) }, 2, "[(1,100) (2,100)]"},
		{func() error { return s.AddS(10, 200) }, 4, "[(1,100) (1,200) (2,100) (2,200)]"},
		{func() error { return s.DelS(10, 100) }, 2, "[(1,200) (2,200)]"},
		{func() error { s.SetR(1, 20); return nil }, 1, "[(2,200)]"},
		{func() error { return s.AddS(20, 300) }, 2, "[(1,300) (2,200)]"},
		{func() error { return s.DelR(2) }, 1, "[(1,300)]"},
	}
	for i, st := range steps {
		if err := st.op(); err != nil {
			check(fmt.Sprintf("step%d", i+1), false, "unexpected err: "+err.Error())
			continue
		}
		got := pairs(s)
		check(fmt.Sprintf("step%d size=%d pairs=%s", i+1, s.JoinSize(), got),
			s.JoinSize() == st.size && got == st.want, "")
	}

	j7 := api.New()
	for _, op := range []func(){
		func() { j7.SetR(1, 10) }, func() { j7.SetR(2, 10) },
		func() { j7.AddS(10, 100) }, func() { j7.AddS(10, 200) },
		func() { j7.DelS(10, 100) }, func() { j7.SetR(1, 20) },
		func() { j7.AddS(20, 300) },
	} {
		op()
	}
	join1 := fmt.Sprint(j7.Join(1))
	j7.DelR(2)
	join2 := fmt.Sprint(j7.Join(2))
	errs := []error{j7.DelR(2), j7.DelS(10, 100), j7.AddS(20, 300)}
	distinct := errors.Is(errs[0], api.ErrNoSuchA) && errors.Is(errs[1], rel.ErrMissingPair) &&
		errors.Is(errs[2], rel.ErrDuplicatePair) && errs[0] != errs[1] && errs[1] != errs[2]
	untraced := j7.JoinSize() == 1 && fmt.Sprint(j7.Join(1)) == "[300]"
	check("joins/errors/no-trace", join1 == "[300]" && join2 == "[]" && distinct && untraced,
		"Join(1)="+join1+" Join(2)="+join2)

	// 复杂度：计数器只在 SelfCheck 内部与 N=100/1000/10000 比较，不向外暴露数值。
	selfOK := api.New().SelfCheck() == nil
	// 并发：一个写者顺序增删，多个读者只读；每次「单个」调用的返回
	// 都必须等于闭环重放中某个写后串行快照的值（单次调用全程持锁，不撕裂）。
	c := api.New()
	wops := []func(){
		func() { c.SetR(0, 0) }, func() { c.AddS(0, 10) },
		func() { c.SetR(1, 0) }, func() { c.AddS(0, 20) },
		func() { c.DelS(0, 10) }, func() { c.SetR(0, 1) },
		func() { c.AddS(1, 30) }, func() { c.DelR(1) },
		func() { c.DelR(0) }, func() { c.DelS(0, 20) }, func() { c.DelS(1, 30) },
	} // 11 步后回到空态，循环重放轨迹完全相同
	okSize := sync.Map{}
	okJoin := [4]sync.Map{}
	record := func() {
		okSize.Store(c.JoinSize(), struct{}{})
		for a := 0; a < 4; a++ {
			okJoin[a].Store(fmt.Sprint(c.Join(a)), struct{}{})
		}
	}
	record()
	for _, op := range wops {
		op()
		record()
	}
	var calls int64
	done := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					n := atomic.AddInt64(&calls, 1)
					if n%5 == 0 {
						if _, ok := okSize.Load(c.JoinSize()); !ok {
							panic("torn JoinSize")
						}
					} else {
						a := int(n % 4)
						if _, ok := okJoin[a].Load(fmt.Sprint(c.Join(a))); !ok {
							panic("torn Join")
						}
					}
				}
			}
		}()
	}
	for rep := 0; rep < 200; rep++ {
		for _, op := range wops {
			op()
		}
	}
	close(done)
	wg.Wait()
	check("complexity/concurrency", selfOK, "SelfCheck + 8 readers, only serial snapshots observed")

	if failures > 0 {
		os.Exit(1)
	}
}
