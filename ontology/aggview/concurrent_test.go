package aggview

import (
	"math/big"
	"sync"
	"testing"
)

// 同一实例“属性写入”与“归属改变”并发：
// 无论内部次序如何，结果必须等价于两种串行顺序之一，
// 不允许旧值计入新分组或新值留在旧分组。
func TestConcurrentPropertyAndMove(t *testing.T) {
	for round := 0; round < 200; round++ {
		e, st, _ := newTestEngine(t)
		registerPayroll(t, e, PolicyFull)
		mustCreate(t, st, "d1", tDept)
		mustCreate(t, st, "d2", tDept)
		mustCreate(t, st, "emp", tEmp)
		_, _ = e.SetProperty("emp", pSalary, FromInt(10))
		_, _ = e.AddLink("emp", lBelong, "d1")

		ver := e.MembershipVersion(vPayroll, "emp")
		var wg sync.WaitGroup
		wg.Add(2)
		var moveErr error
		go func() {
			defer wg.Done()
			_, moveErr = e.MoveToGroup(vPayroll, "emp", "d2", ver)
		}()
		go func() {
			defer wg.Done()
			_, _ = e.SetProperty("emp", pSalary, FromInt(99))
		}()
		wg.Wait()
		if moveErr != nil {
			t.Fatalf("property write must not invalidate the version token: %v", moveErr)
		}

		s1 := e.Query(vPayroll, "d1")
		s2 := e.Query(vPayroll, "d2")
		total := s1.Sum.RatString() + "+" + s2.Sum.RatString()
		// 串行序 A：先迁移后改值 => (d1=0,d2=99)
		// 串行序 B：先改值后迁移 => (d1=0,d2=99)
		// 两个操作作用于同一实例的全部归属时，两种合法串行序结果相同：
		// 新值 99 只能出现在最终归属 d2。
		if total != "0+99" {
			t.Fatalf("round %d: non-serializable outcome d1=%s d2=%s", round, s1.Sum, s2.Sum)
		}
		if s1.Count != 0 || s2.Count != 1 {
			t.Fatalf("round %d: counts d1=%d d2=%d", round, s1.Count, s2.Count)
		}
	}
}

// 两个并发 MoveToGroup 抢同一成员、目标分组不同：只有一个成功，
// 另一个以 KindConflict 被拒绝，且任何时刻聚合总和守恒。
func TestConcurrentMoveConflict(t *testing.T) {
	for round := 0; round < 200; round++ {
		e, st, _ := newTestEngine(t)
		registerPayroll(t, e, PolicyFull)
		mustCreate(t, st, "d1", tDept)
		mustCreate(t, st, "d2", tDept)
		mustCreate(t, st, "d3", tDept)
		mustCreate(t, st, "emp", tEmp)
		_, _ = e.SetProperty("emp", pSalary, FromInt(5))
		_, _ = e.MoveToGroup(vPayroll, "emp", "d1", 0)

		ver := e.MembershipVersion(vPayroll, "emp")
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); _, errs[0] = e.MoveToGroup(vPayroll, "emp", "d2", ver) }()
		go func() { defer wg.Done(); _, errs[1] = e.MoveToGroup(vPayroll, "emp", "d3", ver) }()
		wg.Wait()

		nOK, nConflict := 0, 0
		for _, err := range errs {
			switch ErrorKind(err) {
			case 0:
				nOK++
			case KindConflict:
				nConflict++
			default:
				t.Fatalf("unexpected error %v", err)
			}
		}
		if nOK != 1 || nConflict != 1 {
			t.Fatalf("round %d: want exactly 1 win + 1 conflict, got ok=%d conflict=%d",
				round, nOK, nConflict)
		}
		a := e.Query(vPayroll, "d1")
		b := e.Query(vPayroll, "d2")
		c := e.Query(vPayroll, "d3")
		sum := new(big.Rat).Add(a.Sum, b.Sum)
		sum.Add(sum, c.Sum)
		if sum.Cmp(big.NewRat(5, 1)) != 0 {
			t.Fatalf("round %d: total = %s, want 5 (d1=%s d2=%s d3=%s)",
				round, sum, a.Sum, b.Sum, c.Sum)
		}
	}
}

// 多个不同实例的独立写操作高并发交织，结果必须等价于某种串行顺序：
// 与朴素全量重算逐分组一致。
func TestConcurrentIndependentOps(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	nm := newNaive(st)
	nm.register(e.views[vPayroll].def)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "d2", tDept)
	const n = 40
	for i := 0; i < n; i++ {
		m := ID("c" + itoa(i))
		mustCreate(t, st, m, tEmp)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		m := ID("c" + itoa(i))
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = e.SetProperty(m, pSalary, FromInt(int64(1)))
		}()
		go func() {
			defer wg.Done()
			target := []ID{"d1", "d2"}[i%2]
			ver := e.MembershipVersion(vPayroll, m)
			_, _ = e.MoveToGroup(vPayroll, m, target, ver)
		}()
	}
	wg.Wait()
	assertMatchNaive(t, e, nm)
}
