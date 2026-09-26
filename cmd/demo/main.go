// demo 差分约束求解器演示：逐条打印 OK/FAIL，全部 OK 退出码为 0。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dc"
	"ontology/sp"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

// fixedRounds 错误实现模拟：初始距离全 0，按给定边序固定跑 rounds 轮全量松弛就返回。
func fixedRounds(n int, cs []dc.Constraint, rounds int) []int64 {
	d := make([]int64, n)
	for r := 0; r < rounds; r++ {
		for _, c := range cs {
			if d[c.U]+c.W < d[c.V] {
				d[c.V] = d[c.U] + c.W
			}
		}
	}
	return d
}

func main() {
	// dc：三类非法输入被对应哨兵错误拒绝，合法约束正常入册。
	r, _ := dc.New(3)
	ok := errors.Is(r.Add(0, 3, 1), dc.ErrVarOutOfRange) &&
		errors.Is(r.Add(1, 1, -1), dc.ErrNegativeSelfLoop) &&
		r.Add(0, 1, -2) == nil && r.Count() == 1
	report("dc 校验", ok, "越界/自环负权被拒，合法约束入册")

	// sp：第三节 C1,C2 的点态最大解 [0,-2,-5]；加 C3 成负环，报 ErrInfeasible。
	c12 := []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}}
	got, err := sp.Solve(3, c12)
	ok = err == nil && fmt.Sprint(got) == "[0 -2 -5]"
	report("sp 求解 C1,C2", ok, fmt.Sprintf("x=%v", got))
	c3 := dc.Constraint{U: 2, V: 0, W: -10}
	_, err = sp.Solve(3, append(append([]dc.Constraint{}, c12...), c3))
	report("sp 负环检测", errors.Is(err, sp.ErrInfeasible), "C1+C2+C3 不可行")

	// 三个陷阱（先松 C2 再松 C1）：单轮 x2=-3；C2 取反 x2=0；不检测负环 3 轮得 [-28,-17,-18]。
	ord := []dc.Constraint{c12[1], c12[0]}
	t1 := fixedRounds(3, ord, 1)[2] == -3
	t2 := fixedRounds(3, []dc.Constraint{{U: 1, V: 2, W: 3}, c12[0]}, 5)[2] == 0
	nd := fixedRounds(3, append(append([]dc.Constraint{}, ord...), c3), 3)
	t3 := fmt.Sprint(nd) == "[-28 -17 -18]" && nd[1]-nd[0] > -2
	report("陷阱错值", t1 && t2 && t3, fmt.Sprintf("单轮x2=-3 取反x2=0 不检测负环x=%v 违反C1(11<=-2假)", nd))

	// api：四类哨兵错误互不相同；被拒后状态不变、可继续正常使用。
	e4 := []error{api.ErrNonPositiveN, api.ErrVarOutOfRange, api.ErrNegativeSelfLoop, api.ErrInfeasible}
	seen := map[error]bool{}
	distinct := true
	for _, e := range e4 {
		if seen[e] {
			distinct = false
		}
		seen[e] = true
	}
	s, nerr := api.New(3)
	before := s.ConstraintCount()
	rej := errors.Is(s.AddConstraint(0, 9, 1), api.ErrVarOutOfRange) &&
		errors.Is(s.AddConstraint(2, 2, -5), api.ErrNegativeSelfLoop)
	intact := s.ConstraintCount() == before && s.AddConstraint(0, 1, -2) == nil
	_, nerr2 := s.Solve()
	report("api 错误与状态", distinct && nerr == nil && rej && intact && nerr2 == nil, "四错误互异,被拒不留痕")

	// api.SelfCheck：内置序列核验四条不变量。
	report("api SelfCheck", s.SelfCheck() == nil, "四条不变量通过")

	// 长链 x_{i+1}-x_i<=-1：x_i=-i；全量松弛趟数恒 0 由 sp 包内测试钉住。
	const m = 10000
	chain := make([]dc.Constraint, 0, m-1)
	for i := 0; i+1 < m; i++ {
		chain = append(chain, dc.Constraint{U: i, V: i + 1, W: -1})
	}
	got, err = sp.Solve(m, chain)
	ok = err == nil && got[0] == 0 && got[m-1] == -(m-1)
	report("长链", ok, "m=10000 x_i=-i,全量松弛趟数=0(sp包内测试钉住)")

	// 并发：8 goroutine 对同一约束集并发 Solve，结果逐元素相同。
	sys, _ := api.New(3)
	_ = sys.AddConstraint(0, 1, -2)
	_ = sys.AddConstraint(1, 2, -3)
	want, _ := sys.Solve()
	var wg sync.WaitGroup
	same := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 50; it++ {
				got, err := sys.Solve()
				if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	report("并发 Solve", same, "8x50 次结果逐元素一致")

	if failed {
		os.Exit(1)
	}
}
