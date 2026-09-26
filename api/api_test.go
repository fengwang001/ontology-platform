package api_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/parse"
)

// 三类故障注入各有可判定错误，且互不相同。
func TestErrorsDistinct(t *testing.T) {
	_, errSyntax1 := api.Compile("*a")
	_, errSyntax2 := api.Compile("a**b")
	_, errUnsup := api.Compile("a+")
	_, errLongP := api.Compile(strings.Repeat("a", parse.MaxLen+1))
	m, err := api.Compile("a*b")
	if err != nil {
		t.Fatal(err)
	}
	_, errLongS := m.Match(strings.Repeat("b", parse.MaxLen+1))

	for _, e := range []error{errSyntax1, errSyntax2} {
		if !errors.Is(e, parse.ErrSyntax) {
			t.Errorf("%v 应可判定为 ErrSyntax", e)
		}
	}
	if !errors.Is(errUnsup, parse.ErrUnsupported) {
		t.Errorf("%v 应可判定为 ErrUnsupported", errUnsup)
	}
	for _, e := range []error{errLongP, errLongS} {
		if !errors.Is(e, parse.ErrTooLong) {
			t.Errorf("%v 应可判定为 ErrTooLong", e)
		}
	}
	// 互不相同：任一类不被判成另一类。
	if errors.Is(errSyntax1, parse.ErrUnsupported) || errors.Is(errSyntax1, parse.ErrTooLong) ||
		errors.Is(errUnsup, parse.ErrSyntax) || errors.Is(errUnsup, parse.ErrTooLong) ||
		errors.Is(errLongP, parse.ErrSyntax) || errors.Is(errLongP, parse.ErrUnsupported) {
		t.Error("三类错误不互异")
	}
}

// 不变量 4：被拒后状态不变，仍可继续正常匹配。
func TestRejectLeavesStateUnchanged(t *testing.T) {
	m, err := api.Compile("a*b")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		s    string
		want bool
	}{{"aab", true}, {"b", true}, {"aa", false}, {"", false}}
	before := make([]bool, len(cases))
	for i, c := range cases {
		if before[i], err = m.Match(c.s); err != nil || before[i] != c.want {
			t.Fatalf("拒绝前 Match(%q) = %v,%v", c.s, before[i], err)
		}
	}
	// 各种拒绝：非法模式、不支持字符、模式超长、文本超长。
	_, _ = api.Compile("*a")
	_, _ = api.Compile("a**")
	_, _ = api.Compile("a+")
	_, _ = api.Compile(strings.Repeat("a", parse.MaxLen+1))
	_, _ = m.Match(strings.Repeat("b", parse.MaxLen+1))
	for i, c := range cases {
		got, err := m.Match(c.s)
		if err != nil || got != before[i] {
			t.Fatalf("拒绝后 Match(%q) = %v,%v，拒绝前为 %v", c.s, got, err, before[i])
		}
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("拒绝后 SelfCheck 失败: %v", err)
	}
}

// 并发：N 个 goroutine 对同一已编译模式用不同文本，结果与串行逐对相同。
func TestConcurrentMatchConsistent(t *testing.T) {
	m, err := api.Compile("a*b*.")
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	texts := make([]string, n)
	serial := make([]bool, n)
	for i := range texts {
		texts[i] = strings.Repeat(string(rune('a'+i%3)), i)
		if serial[i], err = m.Match(texts[i]); err != nil {
			t.Fatal(err)
		}
	}
	parallel := make([]bool, n)
	var wg sync.WaitGroup
	for i := range texts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			parallel[i], _ = m.Match(texts[i])
		}(i)
	}
	wg.Wait()
	for i := range serial {
		if serial[i] != parallel[i] {
			t.Errorf("文本 %q：并发 %v != 串行 %v", texts[i], parallel[i], serial[i])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
