package api

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/lex"
	"ontology/parse"
)

// TestEvalTable 表驱动钉住优先级、左结合与向零截断（不变量 3）。
func TestEvalTable(t *testing.T) {
	cases := []struct {
		s    string
		want int64
	}{
		{"8/2/2-3", -1}, {"8-3-2", 3}, {"1-2-3-4", -8}, {"100/10/5", 2},
		{"2+3*4", 14}, {"-7/2", -3}, {"7/-2", -3}, {"0-5/2", -2},
		{"- -3", 3}, {"(1+2)*3", 9}, {"-2*3", -6}, {"-(2+3)*4", -20},
		{"42", 42}, {"10/(3-1)", 5},
	}
	for _, c := range cases {
		if v, err := ParseAndEval(c.s); err != nil || v != c.want {
			t.Fatalf("%q: got %d,%v want %d", c.s, v, err, c.want)
		}
	}
}

// genExpr 生成随机合法表达式（可能含除零子式）。
func genExpr(r *rand.Rand, depth int) string {
	if depth == 0 || r.Intn(3) == 0 {
		return strconv.Itoa(r.Intn(20))
	}
	switch r.Intn(4) {
	case 0:
		return "(" + genExpr(r, depth-1) + ")"
	case 1:
		return "-" + genExpr(r, depth-1)
	default:
		return genExpr(r, depth-1) + []string{"+", "-", "*", "/"}[r.Intn(4)] + genExpr(r, depth-1)
	}
}

// TestAgainstReference 随机表达式与两栈参照求值器对拍（不变量 1、3）。
func TestAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		s := genExpr(r, 4)
		ref, rerr := refEval(s)
		got, gerr := ParseAndEval(s)
		if (rerr == nil) != (gerr == nil) {
			t.Fatalf("%q: ref err %v, got err %v", s, rerr, gerr)
		}
		if rerr == nil && ref != got {
			t.Fatalf("%q: ref %d != got %d", s, ref, got)
		}
	}
}

// TestErrorKinds 四类故障注入各有可判定哨兵错误，且互不相同。
func TestErrorKinds(t *testing.T) {
	cats := []error{lex.ErrLexical, parse.ErrParen, ErrDivZero, parse.ErrOperand, parse.ErrTrailing}
	cases := []struct {
		s    string
		want int // cats 下标
	}{
		{"1@2", 0}, {"99999999999999999999", 0},
		{"(1", 1}, {"1)", 1}, {"()", 1},
		{"1/0", 2}, {"4/(2-2)", 2},
		{"", 3}, {"1+", 3}, {"+1", 3},
		{"1 2", 4},
	}
	for _, c := range cases {
		_, err := ParseAndEval(c.s)
		for i, cat := range cats {
			if errors.Is(err, cat) != (i == c.want) {
				t.Fatalf("%q: err %v, want only category %d", c.s, err, c.want)
			}
		}
	}
}

// TestNoStateAfterFailure 被拒调用不留痕，后续求值结果不变（不变量 4）。
func TestNoStateAfterFailure(t *testing.T) {
	base, err := ParseAndEval("2+3*4")
	if err != nil || base != 14 {
		t.Fatalf("baseline: %d,%v", base, err)
	}
	for _, s := range []string{"1@2", "(1", "1/0", "", "1 2", "1)"} {
		if _, err := ParseAndEval(s); err == nil {
			t.Fatalf("%q must fail", s)
		}
		if v, err := ParseAndEval("2+3*4"); err != nil || v != base {
			t.Fatalf("after %q: got %d,%v want %d", s, v, err, base)
		}
	}
}

// TestASTEvalConsistent 先 Parse 再 Eval 等于 ParseAndEval（不变量 2）。
func TestASTEvalConsistent(t *testing.T) {
	for _, s := range []string{"8/2/2-3", "2+3*4", "-7/2", "(1+2)*3", "- -3", "1-2-3-4"} {
		n, err := Parse(s)
		v1, err1 := Eval(n)
		v2, err2 := ParseAndEval(s)
		if err != nil || err1 != nil || err2 != nil || v1 != v2 {
			t.Fatalf("%q: Eval=%d,%v ParseAndEval=%d,%v", s, v1, err1, v2, err2)
		}
	}
}

// TestConcurrent 多 goroutine 并发求值同一批表达式，结果逐值相同。
func TestConcurrent(t *testing.T) {
	exprs := []string{"8/2/2-3", "2+3*4", "-7/2", "(1+2)*3", "- -3", "100/10/5", "1-2-3-4"}
	want := []int64{-1, 14, -3, 9, 3, 2, -8} // 与 TestEvalTable 一致的真值
	const g = 64
	res := make([][]int64, g)
	var wg sync.WaitGroup
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			res[k] = make([]int64, len(exprs))
			for i, s := range exprs {
				if v, err := ParseAndEval(s); err == nil {
					res[k][i] = v
				} else {
					t.Error(err)
				}
			}
		}(k)
	}
	wg.Wait()
	for k := range res {
		for i := range want {
			if res[k][i] != want[i] {
				t.Fatalf("goroutine %d expr %d: %d != %d", k, i, res[k][i], want[i])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
