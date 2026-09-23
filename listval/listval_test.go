package listval

import (
	"errors"
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr error
	}{
		{"简单列表", "a, b, c", []string{"a", "b", "c"}, nil},
		{"引号内逗号不切", `"a,b", "c"`, []string{`"a,b"`, `"c"`}, nil},
		{"转义引号", `"a\"b,c", d`, []string{`"a\"b,c"`, "d"}, nil},
		{"参数保留", `text/html; charset=utf-8, application/json`, []string{"text/html; charset=utf-8", "application/json"}, nil},
		{"空项丢弃", "a,,b", []string{"a", "b"}, nil},
		{"全空项", ",,,", nil, nil},
		{"首尾空白去除", "  a  ,  b  ", []string{"a", "b"}, nil},
		{"ETag 列表", `"x, y", "z"`, []string{`"x, y"`, `"z"`}, nil},
		{"引号未闭合", `"abc`, nil, ErrUnterminatedQuote},
		{"空字符串", "", nil, nil},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
		if c.wantErr == nil && !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"a, b, c",
		`"a,b", "c\"d,e", f`,
		"a,,b",
		"text/html; q=0.9, application/json",
		"single",
	}
	for _, src := range cases {
		items1, err := Split(src)
		if err != nil {
			t.Fatalf("Split(%q) err %v", src, err)
		}
		items2, err := Split(Join(items1))
		if err != nil {
			t.Fatalf("Split(Join) err %v", err)
		}
		if !slices.Equal(items1, items2) {
			t.Errorf("round trip %q: %q != %q", src, items1, items2)
		}
	}
}
