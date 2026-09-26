// Command demo prints OK/FAIL for each required check of the type checker.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/check"
	"ontology/types"
)

var failed bool

func report(ok bool, what string) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + what)
		return
	}
	fmt.Println("OK " + what)
}

func main() {
	report(types.Int.String() == "int" && types.Bool.String() == "bool" &&
		types.Int.Equal(types.Int) && !types.Int.Equal(types.Bool), "types: int/bool 相等与字符串")

	envX := check.NewEnv().Extend("x", types.Int)
	x := check.VarE("x")
	ifE := check.IfE(check.BinE("<", x, check.IntL(5)),
		check.BinE("+", x, check.IntL(1)), check.IntL(0))
	letE := check.LetE("x", check.IntL(3), ifE)
	steps := []*check.Expr{check.IntL(3), x, check.IntL(5), check.BinE("<", x, check.IntL(5)),
		check.BinE("+", x, check.IntL(1)), check.IntL(0), ifE, letE}
	envs := []*check.Env{check.NewEnv(), envX, envX, envX, envX, envX, envX, check.NewEnv()}
	want := []types.T{types.Int, types.Int, types.Int, types.Bool, types.Int, types.Int, types.Int, types.Int}
	ok, out := true, ""
	for i := range steps {
		t, err := check.Infer(steps[i], envs[i])
		ok = ok && err == nil && t == want[i]
		out += fmt.Sprintf(" %d:%s", i+1, t)
	}
	report(ok, "八步类型:"+out+" (环境 {} → {x:int} → {})")
	t, err := api.Check(letE)
	report(err == nil && t == types.Int, "let x=3 in if x<5 then x+1 else 0 => int")

	_, e1 := api.Check(check.IfE(check.BoolL(true), check.IntL(1), check.BoolL(true)))
	_, e2 := api.Check(check.BinE("<", check.IntL(1), check.BoolL(true)))
	_, e3 := api.Check(check.BinE("+", check.VarE("z"), check.IntL(1)))
	report(errors.Is(e1, check.ErrIf) && errors.Is(e2, check.ErrArithOperand) && errors.Is(e3, check.ErrUndeclared),
		"if true then 1 else true=>ErrIf; 1<true=>ErrArithOperand; z+1=>ErrUndeclared")

	t1, err1 := api.Check(check.BinE("&&", check.BoolL(true), check.BinE("<", check.IntL(1), check.IntL(2))))
	t2, err2 := api.Check(check.BinE("==", check.IntL(1), check.IntL(1)))
	_, err3 := api.Check(check.BinE("==", check.IntL(1), check.BoolL(true)))
	report(err1 == nil && t1 == types.Bool && err2 == nil && t2 == types.Bool && errors.Is(err3, check.ErrEqMismatch),
		"true&&(1<2)=>bool; 1==1=>bool; 1==true=>ErrEqMismatch")

	kinds := []error{check.ErrUndeclared, check.ErrArithOperand, check.ErrLogicOperand, check.ErrIf}
	distinct := true
	for i := range kinds {
		for j := range kinds {
			if (i == j) != errors.Is(kinds[i], kinds[j]) {
				distinct = false
			}
		}
	}
	t3, err4 := api.Check(letE) // 多次被拒之后
	report(distinct && err4 == nil && t3 == types.Int && api.SelfCheck() == nil,
		"四类错误互不相同; 被拒后状态不变; SelfCheck 通过")

	ok = true
	for _, m := range []int{100, 1000, 10000} {
		bind := map[string]types.T{}
		for i := 0; i < m; i++ {
			bind[fmt.Sprintf("n%d", i)] = types.Int
		}
		_, found := check.NewEnvFrom(bind).Lookup(fmt.Sprintf("n%d", m-1))
		ok = ok && found
	}
	report(ok, "大 m 查找走哈希定位, 检查个数不随 m 增长(数值断言见 TestProbeCountConstant)")

	report(concurrent(letE), "32 goroutine 并发 Check 结果逐值一致")
	if failed {
		os.Exit(1)
	}
}

func concurrent(letE *check.Expr) bool {
	battery := []*check.Expr{letE,
		check.IfE(check.BoolL(true), check.IntL(1), check.BoolL(true)),
		check.BinE("==", check.IntL(1), check.IntL(1)),
		check.BinE("+", check.VarE("z"), check.IntL(1))}
	type res struct {
		t types.T
		s string
	}
	base := make([]res, len(battery))
	for i, e := range battery {
		t, err := api.Check(e)
		base[i] = res{t, fmt.Sprint(err)}
	}
	var wg sync.WaitGroup
	bad := make(chan bool, 32*len(battery))
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, e := range battery {
				t, err := api.Check(e)
				if (res{t, fmt.Sprint(err)}) != base[i] {
					bad <- true
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	_, mismatch := <-bad
	return !mismatch
}
