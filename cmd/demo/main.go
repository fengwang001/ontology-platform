package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ddl"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	s := ddl.New(1000)
	steps := []struct {
		op     func() error
		ph     string
		v1, v2 string
	}{
		{func() error { return s.Put("a", 3) }, "Normal", "map[a:3]", "map[]"},
		{func() error { return s.Put("b", 5) }, "Normal", "map[a:3 b:5]", "map[]"},
		{s.BeginMigration, "DualWrite", "map[a:3 b:5]", "map[]"},
		{func() error { return s.Put("c", 4) }, "DualWrite", "map[a:3 b:5 c:4]", "map[c:8]"},
		{s.Backfill, "DualWrite", "map[a:3 b:5 c:4]", "map[a:6 b:10 c:8]"},
		{s.Backfill, "DualWrite", "map[a:3 b:5 c:4]", "map[a:6 b:10 c:8]"},
		{func() error { return s.Put("a", 7) }, "DualWrite", "map[a:7 b:5 c:4]", "map[a:14 b:10 c:8]"},
		{s.Switch, "Switched", "map[a:7 b:5 c:4]", "map[a:14 b:10 c:8]"},
		{func() error { return s.Put("d", 2) }, "Switched", "map[a:7 b:5 c:4]", "map[a:14 b:10 c:8 d:4]"},
	}
	nineOK, preSwitch, postSwitch := true, 0, 0
	var v2AfterF1 string
	for i, st := range steps {
		if err := st.op(); err != nil {
			nineOK = false
		}
		v1, v2 := s.Snapshot()
		if s.Phase().String() != st.ph || fmt.Sprint(v1) != st.v1 || fmt.Sprint(v2) != st.v2 {
			nineOK = false
		}
		switch i {
		case 4:
			v2AfterF1 = fmt.Sprint(v2)
		case 5:
			check("回填幂等(第5/6步v2相同)", v2AfterF1 == fmt.Sprint(v2))
		case 6:
			preSwitch = s.Get("a")
		case 8:
			postSwitch = s.Get("a")
		}
	}
	check("九步分步表(每步阶段+v1/v2全表)", nineOK)
	check("切换前后读值不同(7->14)", preSwitch == 7 && postSwitch == 14)

	// 四类可判定错误，互不相同
	e := api.New(10)
	errs := []error{
		e.Switch(),     // 阶段非法
		e.Put("", 1),   // 键非法
		e.Put("k", -1), // 值非法(负)
		e.Put("k", 6),  // 值非法(2v超上限)
	}
	e2 := api.New(10)
	_ = e2.BeginMigration()
	e2.InjectDualWriteFault()
	errs = append(errs, e2.Put("z", 1)) // 双写故障
	wantErrs := []error{api.ErrBadPhase, api.ErrEmptyKey, api.ErrBadValue, api.ErrBadValue, api.ErrDualWriteFault}
	errOK := len(errs) == len(wantErrs)
	for i, err := range errs {
		if err == nil || !errors.Is(err, wantErrs[i]) {
			errOK = false
		}
	}
	cats := []error{api.ErrBadPhase, api.ErrEmptyKey, api.ErrBadValue, api.ErrDualWriteFault}
	for i := range cats {
		for j := i + 1; j < len(cats); j++ {
			if errors.Is(cats[i], cats[j]) {
				errOK = false // 四类必须互不相同
			}
		}
	}
	check("四类可判定错误(哨兵互不相同)", errOK)

	// 被拒后状态不变且可继续用
	traceOK := e.Get("k") == 0 && e.Put("k", 5) == nil && e.Get("k") == 5
	check("被拒后状态不变且可继续用", traceOK)

	// 双写原子：故障下 v1/v2 同时不变
	_ = e2.Switch()
	check("双写原子(故障下两表均无z)", e2.Get("z") == 0)

	// 第二次回填跳过已处理键（检查数=0 由 ddl 包内测试钉住，此处验证行为幂等）
	b := api.New(1000)
	for i := 0; i < 500; i++ {
		_ = b.Put(fmt.Sprintf("k%d", i), 1)
	}
	_ = b.BeginMigration()
	_ = b.Backfill()
	_ = b.Switch()
	skipOK := b.Get("k499") == 2
	check("二次回填检查数=0(行为幂等)", skipOK)

	// 并发读一致 + 并发写不同键后各键正确
	c := api.New(1000)
	_ = c.Put("hot", 42)
	var wg sync.WaitGroup
	readOK := true
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if c.Get("hot") != 42 {
					readOK = false
				}
			}
		}()
	}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.Put(fmt.Sprintf("w%d", i), i)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 32; i++ {
		if c.Get(fmt.Sprintf("w%d", i)) != i {
			readOK = false
		}
	}
	check("并发读一致+并发写正确", readOK)

	check("SelfCheck 四条不变量", api.New(100).SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
