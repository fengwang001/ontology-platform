package api

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/lex"
	"ontology/parse"
)

func TestEvalTable(t *testing.T) {
	for in, want := range map[string]int64{
		"8/2/2-3": -1, "2+3*4": 14, "-7/2": -3, "8-3-2": 3,
		"- -3": 3, "(1+2)*3": 9, " 8/2/2-3 ": -1, "7/-2": -3,
		"2*-3": -6, "---5": -5, "-(2+3)*2": -10, "10/3": 3,
	} {
		if got, err := ParseAndEval(in); err != nil || got != want {
			t.Errorf("Eval(%q)=%d,%v want %d", in, got, err, want)
		}
	}
}

// TestParseEvalConsistency 钉住不变量 2：Parse 后再 Eval 等于直接求值。
func TestParseEvalConsistency(t *testing.T) {
	for _, s := range []string{"8/2/2-3", "-7/2", "(1+2)*3", "2+3*4-6/2", "- -3"} {
		direct, e1 := ParseAndEval(s)
		n, e2 := Parse(s)
		via, e3 := Eval(n)
		if e1 != nil || e2 != nil || e3 != nil || via != direct {
			t.Errorf("%q: direct=%d ast=%d errs=%v,%v,%v", s, direct, via, e1, e2, e3)
		}
	}
}

// TestLeftAssoc 钉住不变量 3：同级二元运算从最左结合，且与参照一致。
func TestLeftAssoc(t *testing.T) {
	for in, want := range map[string]int64{"8/2/2": 2, "8-3-2": 3, "100/10/2": 5, "20-8-5-2": 5} {
		got, _ := ParseAndEval(in)
		toks, _ := lex.Tokenize(in)
		ref, _ := referenceEval(toks)
		if got != want || ref != want {
			t.Errorf("%s got=%d ref=%d want=%d", in, got, ref, want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]error{
		"1@2": lex.ErrIllegalChar, "999999999999999999999999": lex.ErrNumberOverflow,
		"(1+2": parse.ErrParen, "1)": parse.ErrParen, ")": parse.ErrParen,
		"1/0": ErrDivZero, "2/(3-3)": ErrDivZero,
		"": parse.ErrEmpty, "1 2": parse.ErrTrailing,
	}
	cls := map[error]bool{}
	for in, want := range cases {
		if _, err := ParseAndEval(in); !errors.Is(err, want) {
			t.Errorf("Eval(%q) err=%v want %v", in, err, want)
		}
		cls[want] = true
	}
	if len(cls) < 4 { // 各哨兵互不相同（同类多用例共享同一哨兵）
		t.Errorf("need >=4 distinct sentinels, got %d", len(cls))
	}
}

// TestNoStateAfterReject 钉住不变量 4：拒绝不留痕，后续调用结果稳定。
func TestNoStateAfterReject(t *testing.T) {
	bad := []string{"1@2", "(1", "1/0", "", "1 2", "999999999999999999999999"}
	for i := 0; i < 50; i++ {
		if _, err := ParseAndEval(bad[i%len(bad)]); err == nil {
			t.Fatalf("iter %d: bad input accepted", i)
		}
		if v, err := ParseAndEval("(1+2)*3"); err != nil || v != 9 {
			t.Fatalf("iter %d: state leaked %d,%v", i, v, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// gen 依文法生成：lv 2=expr(+-)、1=term(*/)、0=factor；d 为剩余深度，保证终止。
func gen(r *rand.Rand, lv, d int) string {
	if lv == 0 {
		if d <= 0 || r.Intn(3) == 0 {
			return strconv.Itoa(r.Intn(10))
		}
		if r.Intn(2) == 0 {
			return "(" + gen(r, 2, d-1) + ")"
		}
		return "-" + gen(r, 0, d-1)
	}
	s := gen(r, lv-1, d)
	ops := "+-"
	if lv == 1 {
		ops = "*/"
	}
	for k := r.Intn(3); k > 0; k-- {
		s += string(ops[r.Intn(2)]) + gen(r, lv-1, d)
	}
	return s
}

// TestReferenceRandom 钉住不变量 1：随机合法串与两栈参照结果一致。
func TestReferenceRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		s := gen(r, 2, 3)
		direct, dErr := ParseAndEval(s)
		toks, _ := lex.Tokenize(s)
		ref, rErr := referenceEval(toks)
		if (dErr == nil) != (rErr == nil) || dErr == nil && direct != ref {
			t.Fatalf("%q: direct=(%d,%v) ref=(%d,%v)", s, direct, dErr, ref, rErr)
		}
	}
}

// TestConcurrent：64 个 goroutine 并发求同一批表达式，逐值必须相同（无 sleep）。
func TestConcurrent(t *testing.T) {
	exprs := []string{"8/2/2-3", "2+3*4", "-7/2", "(1+2)*3", "- -3", "100/2/5"}
	const ng = 64
	var wg sync.WaitGroup
	got := make([][]int64, ng)
	for g := 0; g < ng; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			row := make([]int64, len(exprs))
			for i, s := range exprs {
				row[i], _ = ParseAndEval(s)
			}
			got[g] = row
		}(g)
	}
	wg.Wait()
	for g := 1; g < ng; g++ {
		if !reflect.DeepEqual(got[g], got[0]) {
			t.Fatalf("goroutine %d = %v, want %v", g, got[g], got[0])
		}
	}
}
