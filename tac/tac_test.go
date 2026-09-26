package tac

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/ast"
)

func TestRejectedInputs(t *testing.T) {
	for n, want := range map[*ast.Expr]error{
		nil:                                  ErrNilNode,
		ast.Bin("%", ast.Int(1), ast.Int(2)): ErrUnknownOp,
		&ast.Expr{Kind: ast.Unary, Op: "~", Left: ast.Int(1)}: ErrUnknownOp,
		ast.IfElse(ast.Bool(true), nil, ast.Int(2)):           ErrMissingBranch,
		ast.IfElse(ast.Bool(true), ast.Int(1), nil):           ErrMissingBranch,
	} {
		if is, err := Gen(n); !errors.Is(err, want) || is != nil {
			t.Fatalf("is=%v err=%v want %v", is, err, want)
		}
	}
	if _, err := Gen(nil); err == nil {
		t.Fatal("nil accepted after a rejection")
	}
	if is, err := Gen(ast.Int(1)); err != nil || len(is) != 1 || is[0].Result != "t1" {
		t.Fatalf("state not clean after reject: %v %v", is, err)
	}
}

func checkNumbering(t *testing.T, n *ast.Expr) []Instr {
	t.Helper()
	is, err := Gen(n)
	if err != nil {
		t.Fatal(err)
	}
	tn, ln, seen := 0, 0, map[string]bool{}
	for _, x := range is {
		if r := x.Result; r != "" && !seen[r] {
			seen[r] = true
			tn++
			if r != fmt.Sprintf("t%d", tn) {
				t.Fatalf("temp order: %s != t%d", r, tn)
			}
		}
		if x.Op == OpLabel {
			ln++
			if x.Label != fmt.Sprintf("L%d", ln) {
				t.Fatalf("label order: %s != L%d", x.Label, ln)
			}
		}
	}
	return is
}

func TestTempNumbering(t *testing.T) {
	a, b, c := ast.V("a"), ast.V("b"), ast.V("c")
	for _, n := range []*ast.Expr{
		ast.Bin("||", ast.Bin("&&", a, b), c),
		ast.Bin("+", ast.IfElse(c, ast.Int(1), ast.Int(2)), ast.Int(3)),
		ast.IfElse(ast.Bin("&&", a, b), ast.Neg(c), ast.Not(a)),
	} {
		checkNumbering(t, n)
	}
}

func TestResultTemp(t *testing.T) {
	a, b := ast.V("a"), ast.V("b")
	for i, n := range []*ast.Expr{
		ast.Bin("+", a, b),
		ast.Bin("||", ast.Bin("&&", a, b), ast.V("c")),
		ast.IfElse(a, ast.Int(1), ast.Bin("*", b, ast.Int(2))),
	} {
		is := checkNumbering(t, n)
		last, res, lastWrite := map[string]int{}, "", -1
		for k, x := range is {
			if x.Result != "" {
				res = x.Result
				last[x.Result] = k
				lastWrite = k
			}
		}
		if last[res] != lastWrite {
			t.Fatalf("case %d: result %s last defined at %d, stream ends writes at %d", i, res, last[res], lastWrite)
		}
	}
}

func TestShortCircuitNoDeadCode(t *testing.T) {
	dz := ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0))
	for _, tc := range []struct {
		op string
		j  Op
	}{
		{"&&", OpJmpFalse},
		{"||", OpJmpTrue},
	} {
		left := ast.Bool(tc.op == "||") // false short-circuits &&; true short-circuits ||
		is := checkNumbering(t, ast.Bin(tc.op, left, dz))
		jp := -1
		for k, x := range is {
			if x.Op == tc.j {
				jp = k
				break
			}
		}
		lp := len(is) - 1
		div := false
		for k := jp + 1; k < lp; k++ {
			if is[k].BinOp == "/" {
				div = true
			}
		}
		if jp < 0 || !div || is[lp].Op != OpLabel {
			t.Fatalf("%s: division must live only inside jump..label span (jp=%d lp=%d div=%v)", tc.op, jp, lp, div)
		}
	}
}

func linearPeak(is []Instr) int {
	live, peak := map[string]bool{}, 0
	for _, x := range is {
		for _, o := range [2]string{x.A, x.B} {
			if strings.HasPrefix(o, "t") {
				delete(live, o)
			}
		}
		if x.Result != "" {
			live[x.Result] = true
		}
		peak = max(peak, len(live))
	}
	return peak
}

func TestLeftChainPeakLive(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		chain := ast.V("v0")
		for i := 1; i < m; i++ {
			chain = ast.Bin("+", chain, ast.V(fmt.Sprintf("v%d", i)))
		}
		is := checkNumbering(t, chain)
		g := &generator{} // white-box read of the unexported counter
		g.gen(chain)
		if p := linearPeak(is); p > 3 || g.err != nil || g.peakLive > 3 {
			t.Fatalf("m=%d stream peak=%d internal peak=%d err=%v", m, p, g.peakLive, g.err)
		}
	}
}
