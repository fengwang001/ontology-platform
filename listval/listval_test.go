package listval

import (
	"errors"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
		err  error
	}{
		{"单元素", "a", []string{"a"}, nil},
		{"基本切分", "a, b,c", []string{"a", "b", "c"}, nil},
		{"元素首尾空白去除", "  a  ,\tb\t", []string{"a", "b"}, nil},
		{"引号内逗号不切", `a, "b,c", d`, []string{"a", `"b,c"`, "d"}, nil},
		{"转义引号不闭合字符串", `"a\"b,c", d`, []string{`"a\"b,c"`, "d"}, nil},
		{"参数中的引号逗号", `text/plain;level="1,2", x`, []string{`text/plain;level="1,2"`, "x"}, nil},
		{"中间空项报错", "a,,b", nil, ErrEmptyItem},
		{"首空项报错", ",a", nil, ErrEmptyItem},
		{"尾空项报错", "a,", nil, ErrEmptyItem},
		{"空白空项报错", "a,  ,b", nil, ErrEmptyItem},
		{"空值报错", "", nil, ErrEmptyItem},
		{"未闭合引号报错", `"abc`, nil, ErrUnclosedQuote},
		{"结尾转义报错", `"abc\`, nil, ErrUnclosedQuote},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: Split(%q) err = %v, want %v", c.name, c.in, err, c.err)
			continue
		}
		if c.err == nil && strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: Split(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestJoin(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		want  string
		err   error
	}{
		{"基本合并", []string{"a", "b"}, "a, b", nil},
		{"合并前 trim", []string{" a ", "b"}, "a, b", nil},
		{"引号原样保留", []string{`"x,y"`, "z"}, `"x,y", z`, nil},
		{"空元素报错", []string{"a", ""}, "", ErrEmptyItem},
		{"空白元素报错", []string{"a", "  "}, "", ErrEmptyItem},
	}
	for _, c := range cases {
		got, err := Join(c.items)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%s: Join(%q) = %q, %v; want %q, %v", c.name, c.items, got, err, c.want, c.err)
		}
	}
}

// TestRoundTrip 切分→合并→再切分必须得到同一元素序列（语义等价）。
func TestRoundTrip(t *testing.T) {
	cases := []string{
		"a, b, c",
		`text/html, text/plain;level="1,2", application/json`,
		`"quoted, item", plain, "esc\"aped, too"`,
		"a,b,c,d,e",
	}
	for _, src := range cases {
		items, err := Split(src)
		if err != nil {
			t.Fatalf("Split(%q): %v", src, err)
		}
		joined, err := Join(items)
		if err != nil {
			t.Fatalf("Join(%q): %v", items, err)
		}
		again, err := Split(joined)
		if err != nil {
			t.Fatalf("Split(%q): %v", joined, err)
		}
		if strings.Join(items, "|") != strings.Join(again, "|") {
			t.Errorf("往返不等价: %q -> %q -> %q", items, joined, again)
		}
	}
}
