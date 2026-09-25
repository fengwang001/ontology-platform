package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

var table = []struct{ in, want string }{
	{"3.14", "157/50"}, {"0.1", "1/10"}, {"-2.5", "-5/2"}, {"0.(3)", "1/3"},
	{"0.1(6)", "1/6"}, {"123", "123/1"}, {"0.(142857)", "1/7"}, {"-0.0", "0/1"},
	{"0.(0)", "0/1"}, {"1.(285714)", "9/7"}, {"1.0", "1/1"}, {"0", "0/1"},
	{"-7", "-7/1"}, {"10.(9)", "11/1"}, {"0.5", "1/2"}, {"-0.(3)", "-1/3"},
}

func TestParseTable(t *testing.T) {
	for _, c := range table {
		f, err := Parse(c.in)
		if err != nil || f.String() != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %s", c.in, f, err, c.want)
		}
	}
}

// 不变量 1：与 big 朴素参照逐条一致（含循环生成的多档输入）。
func TestAgainstBigRef(t *testing.T) {
	cases := []string{}
	for _, c := range table {
		cases = append(cases, c.in)
	}
	for i := 1; i <= 50; i++ {
		cases = append(cases, fmt.Sprintf("%d.%d", i, i*7), fmt.Sprintf("-%d.(%d)", i, i))
	}
	for _, s := range cases {
		f, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		ref := bigRef(s)
		if f.N != ref.N || f.D != ref.D {
			t.Errorf("Parse(%q) = %s, big ref = %d/%d", s, f, ref.N, ref.D)
		}
	}
}

// 不变量 2：既约且规范（d>0、gcd==1、零为 0/1）。
func TestNormalized(t *testing.T) {
	for _, c := range table {
		f, _ := Parse(c.in)
		if f.D <= 0 || gcd(abs(f.N), f.D) != 1 {
			t.Errorf("%q: %s not reduced/normalized", c.in, f)
		}
		if f.N == 0 && f.D != 1 {
			t.Errorf("%q: zero not normalized: %s", c.in, f)
		}
	}
}

// 不变量 3：循环小数精确，不得浮点近似。
func TestExactRepeating(t *testing.T) {
	for in, want := range map[string]string{"0.(3)": "1/3", "0.1(6)": "1/6", "0.(142857)": "1/7"} {
		f, _ := Parse(in)
		if f.String() != want {
			t.Errorf("Parse(%q) = %s, want %s", in, f, want)
		}
	}
}

// 不变量 4：被拒输入整体失败、不留痕，之后仍可正常使用。
func TestRejectedLeavesNoState(t *testing.T) {
	bad := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty}, {"1a", ErrBadChar}, {"1..2", ErrSyntax}, {"1(2)(3)", ErrSyntax},
		{"1-2", ErrSyntax}, {".5", ErrSyntax}, {"()", ErrSyntax}, {"1.(2)3", ErrSyntax},
		{"9999999999999999999", ErrOverflow}, {"0.9999999999999999999", ErrOverflow},
	}
	for _, b := range bad {
		if _, err := Parse(b.in); !errors.Is(err, b.want) {
			t.Errorf("Parse(%q) err = %v, want %v", b.in, err, b.want)
		}
		f, err := Parse("3.14") // 每次被拒后正常解析不受影响
		if err != nil || f.String() != "157/50" {
			t.Fatalf("after rejecting %q: state corrupted (%v, %v)", b.in, f, err)
		}
	}
}

// 四类哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	errs := []error{ErrEmpty, ErrBadChar, ErrSyntax, ErrOverflow}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors %d and %d not distinct", i, j)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 并发：N 个 goroutine 对同一批字符串调 Parse，结果与串行逐条相同。
func TestConcurrent(t *testing.T) {
	batch := []string{}
	for _, c := range table {
		batch = append(batch, c.in)
	}
	serial := map[string]string{}
	for _, s := range batch {
		f, _ := Parse(s)
		serial[s] = f.String()
	}
	const N = 64
	var wg sync.WaitGroup
	errs := make([]error, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p := New()
			for i := 0; i < 100; i++ {
				for _, s := range batch {
					f, err := p.Parse(s)
					if err != nil || f.String() != serial[s] {
						errs[g] = fmt.Errorf("%q: %v, %v", s, f, err)
						return
					}
				}
			}
			if err := p.SelfCheck(); err != nil {
				errs[g] = err
			}
		}(g)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
