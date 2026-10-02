package module

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentEvaluate 并发调用所有操作：结果等价于某个串行顺序，
// 一次 Evaluate 是一个原子步骤，任何观察者看不到中间状态。
func TestConcurrentEvaluate(t *testing.T) {
	s := NewSession()
	// 预登记一批互相依赖的模块（含循环）。
	const n = 20
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("M%d", i)
		next := fmt.Sprintf("M%d", (i+1)%n)
		nextF := fmt.Sprintf("f%d", (i+1)%n)
		var imports []Import
		var body []Step
		if i > 0 {
			imports = []Import{imp(next, nextF)}
			body = []Step{Read(next, nextF), Init("x")}
		} else {
			body = []Step{Init("x")}
		}
		mustAdd(t, s, name, imports, cat(funcs(fmt.Sprintf("f%d", i)), lets("x")), nil, body)
	}

	var wg sync.WaitGroup
	// 并发求值各个根。
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 5; k++ {
				s.Evaluate(fmt.Sprintf("M%d", (i+k)%n))
			}
		}(i)
	}
	// 并发查询。
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 10; k++ {
				s.Status(fmt.Sprintf("M%d", (i+k)%n))
				s.Order()
			}
		}(i)
	}
	// 并发登记新模块（名字互不重复）。
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("N%d", i)
			s.AddModule(name, []Import{imp("M0", "f0")}, nil, nil, nil)
			s.Evaluate(name)
		}(i)
	}
	wg.Wait()

	// 原子性不变量：次序中每个已求值模块恰出现一次，无求值中，体至多执行一次。
	checkInvariants(t, s)
	// 出错模块永不重试、已求值模块不再执行：再并发求值一轮，体执行次数不得增加。
	before := capture(s)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Evaluate(fmt.Sprintf("M%d", i))
		}(i)
	}
	wg.Wait()
	after := capture(s)
	for name, runs := range before.bodyRuns {
		if after.bodyRuns[name] != runs {
			t.Fatalf("模块 %s 的体执行次数从 %d 变为 %d", name, runs, after.bodyRuns[name])
		}
	}
	checkInvariants(t, s)
}

// TestConcurrentDeterministicReplay 同一操作序列在两个会话上重放，结果完全相同。
func TestConcurrentDeterministicReplay(t *testing.T) {
	build := func() *Session {
		s := NewSession()
		addCycleAB(t, s)
		mustAdd(t, s, "C", []Import{imp("A", "x")}, nil, nil, nil)
		return s
	}
	s1, s2 := build(), build()
	ops := []string{"A", "C", "A", "B"}
	for _, root := range ops {
		r1, j1 := s1.Evaluate(root)
		r2, j2 := s2.Evaluate(root)
		mustSameReject(t, "重放拒绝", j1, j2)
		mustSameResult(t, "重放结果", r1, r2)
	}
	if !checkSnapshotsEqual(capture(s1), capture(s2)) {
		t.Fatalf("重放后状态不一致:\n%+v\n%+v", capture(s1), capture(s2))
	}
}

func checkSnapshotsEqual(a, b snapshot) bool {
	if len(a.states) != len(b.states) || len(a.order) != len(b.order) {
		return false
	}
	for k, v := range a.states {
		if b.states[k] != v || a.errs[k] != b.errs[k] || a.bodyRuns[k] != b.bodyRuns[k] {
			return false
		}
		for name, bit := range a.inited[k] {
			if b.inited[k][name] != bit {
				return false
			}
		}
	}
	for i, v := range a.order {
		if b.order[i] != v {
			return false
		}
	}
	return true
}
