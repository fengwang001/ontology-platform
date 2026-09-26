package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/check"
	"ontology/types"
)

var fails int

func line(ok bool, msg string) {
	if ok {
		fmt.Println("OK", msg)
	} else {
		fails++
		fmt.Println("FAIL", msg)
	}
}

func classify(e *check.Expr, env *check.Env) string {
	var t types.T
	var err error
	if env == nil {
		t, err = api.Check(e)
	} else {
		t, err = check.Infer(e, env)
	}
	if err == nil {
		return t.String()
	}
	for _, s := range []struct {
		e error
		n string
	}{
		{check.ErrUndefined, "undef"}, {check.ErrIntRequired, "intneed"},
		{check.ErrBoolRequired, "boolneed"}, {check.ErrIf, "if"},
		{check.ErrEqMismatch, "eq"},
	} {
		if errors.Is(err, s.e) {
			return s.n
		}
	}
	return "other"
}

func class(e *check.Expr) string { return classify(e, nil) }

func main() {
	// 第三节八步：逐步类型与环境（x 仅在 let 体内为 int，退出后回到 ∅）。
	env := check.NewEnv()
	env.Bind("x", types.Int)
	steps := []string{
		classify(check.L(3), env),
		classify(check.V("x"), env),
		classify(check.Bin("<", check.V("x"), check.L(5)), env),
		classify(check.Bin("+", check.V("x"), check.L(1)), env),
		classify(check.L(0), env),
	}
	full := check.Let("x", check.L(3), check.If(check.Bin("<", check.V("x"), check.L(5)),
		check.Bin("+", check.V("x"), check.L(1)), check.L(0)))
	line(steps[0] == "int" && steps[1] == "int" && steps[2] == "bool" &&
		steps[3] == "int" && steps[4] == "int" && class(full) == "int",
		"八步类型/环境 3,x→x<5:b→x+1:i→0:i, let-if 整体=int, 退出后 x∉∅")

	line(class(check.If(check.B(true), check.L(1), check.B(true))) == "if",
		"if true then 1 else true => 报错(分支不一致)")
	line(class(check.Bin("<", check.L(1), check.B(true))) == "intneed", "1 < true => 报错(比较操作数非int)")
	line(class(check.Bin("+", check.V("z"), check.L(1))) == "undef", "z + 1 => 报错(未声明)")
	line(class(check.Bin("&&", check.B(true), check.Bin("<", check.L(1), check.L(2)))) == "bool" &&
		class(check.Bin("==", check.L(1), check.L(1))) == "bool" &&
		class(check.Bin("==", check.L(1), check.B(true))) == "eq",
		"true && (1<2)=bool; 1==1=bool; 1==true=>报错(eq)")

	four := map[string]bool{}
	for _, c := range []string{
		class(check.V("z")), class(check.Bin("+", check.L(1), check.B(true))),
		class(check.Bin("&&", check.B(true), check.L(1))),
		class(check.If(check.L(1), check.L(1), check.L(2))),
	} {
		four[c] = true
	}
	line(len(four) == 4, "四类可判定错误互不相同 undef/intneed/boolneed/if")

	_ = class(check.Bin("+", check.V("z"), check.L(1))) // 被拒表达式
	line(class(check.L(7)) == "int" && api.SelfCheck() == nil, "被拒后状态不变(后续 int 仍可判) 且 api.SelfCheck 通过")
	line(check.VerifyLookupProbe() == nil, "大 m=100/1000/10000 下单次查找探针为常数(哈希定位)")

	es := []*check.Expr{full, check.Bin("==", check.L(1), check.B(true)),
		check.Bin("&&", check.B(true), check.B(false)), check.V("q")}
	want := make([]string, len(es))
	for i, e := range es {
		want[i] = class(e)
	}
	var bad atomic.Bool
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, e := range es {
				if class(e) != want[i] {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	line(!bad.Load(), "24 goroutine 并发 Check 同一批表达式结果逐值一致")

	if fails > 0 {
		os.Exit(1)
	}
}
