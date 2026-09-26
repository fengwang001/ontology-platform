package api

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/lex"
	"ontology/pratt"
)

// TestInvariantRef：固定表 + 200 条随机表达式，Eval 与参照 refEval 结果一致（不变量 1）。
func TestInvariantRef(t *testing.T) {
	exprs := []string{"10-4-3", "2^3^2", "-2^2", "2^10", "- -3", "1- -2", "(1+2)^2", "1+2*3^2"}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		exprs = append(exprs, genExpr(r, 4))
	}
	for _, s := range exprs {
		v1, e1 := Eval(s)
		v2, e2 := refEval(s)
		if (e1 == nil) != (e2 == nil) || (e1 == nil && v1 != v2) {
			t.Errorf("%q: Eval=%d,%v refEval=%d,%v", s, v1, e1, v2, e2)
		}
	}
}

// genExpr 随机生成表达式；^ 的指数限制为个位数以控制溢出比例。
func genExpr(r *rand.Rand, depth int) string {
	if depth <= 0 {
		return strconv.Itoa(r.Intn(100))
	}
	switch r.Intn(6) {
	case 0:
		return "-" + genExpr(r, depth-1)
	case 1:
		return "(" + genExpr(r, depth-1) + ")"
	case 2:
		return genExpr(r, depth-1) + "^" + strconv.Itoa(r.Intn(5))
	}
	op := []string{"+", "-", "*", "/"}[r.Intn(4)]
	return genExpr(r, depth-1) + op + genExpr(r, depth-1)
}

// TestErrors：各类非法输入都有可判定哨兵错误，且四类故障注入互不相同（不变量 4）。
func TestErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"1&2", lex.ErrIllegalChar},
		{"(1+2", pratt.ErrParen},
		{"2^-1", pratt.ErrBadExp},
		{"1/0", pratt.ErrDivZero},
		{"99999999999999999999999", lex.ErrNumRange},
		{"10^19", pratt.ErrOverflow},
		{"", pratt.ErrSyntax},
	}
	for _, c := range cases {
		if _, err := Eval(c.in); !errors.Is(err, c.want) {
			t.Errorf("%q: err=%v; want %v", c.in, err, c.want)
		}
	}
	four := []error{lex.ErrIllegalChar, pratt.ErrParen, pratt.ErrBadExp, pratt.ErrDivZero}
	for i, a := range four {
		for j, b := range four {
			if i != j && errors.Is(a, b) {
				t.Errorf("哨兵错误不互异：%v vs %v", a, b)
			}
		}
	}
}

// TestRejectedThenOK：被拒后不影响后续调用。
func TestRejectedThenOK(t *testing.T) {
	for _, s := range []string{"1&2", "(1", "2^-1", "1/0"} {
		if _, err := Eval(s); err == nil {
			t.Fatalf("%q 应被拒绝", s)
		}
		if v, err := Eval("6*7"); err != nil || v != 42 {
			t.Fatalf("拒绝 %q 后状态被污染：%d, %v", s, v, err)
		}
	}
}

// TestConcurrent：64 个 goroutine 并发求值同一批表达式，结果逐值相同。
func TestConcurrent(t *testing.T) {
	exprs := map[string]int64{
		"2^3^2": 512, "-2^2": 4, "(1+2)^2": 9, "10-4-3": 3, "2^10": 1024, "1- -2": 3,
	}
	const g = 64
	var wg sync.WaitGroup
	errs := make(chan string, g*len(exprs))
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s, want := range exprs {
				if v, err := Eval(s); err != nil || v != want {
					errs <- fmt.Sprintf("%s=%d,%v want %d", s, v, err, want)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
