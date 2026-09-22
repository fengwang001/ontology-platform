package rangespec_test

import (
	"errors"
	"testing"

	"ontology/coalesce"
	"ontology/rangespec"
)

func TestParseThreeForms(t *testing.T) {
	got, err := rangespec.Parse("bytes=10-20,30-,-40")
	if err != nil {
		t.Fatal(err)
	}
	want := []rangespec.Spec{
		{Kind: rangespec.FromTo, Start: 10, End: 20},
		{Kind: rangespec.FromEnd, Start: 30},
		{Kind: rangespec.Suffix, SuffixN: 40},
	}
	if len(got) != 3 {
		t.Fatalf("got %d specs, want 3", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("spec %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSyntaxErrorOffset(t *testing.T) {
	cases := map[string]int{
		"byte=0-1":      0, // 缺少前缀
		"bytes=":       0, // 空列表
		"bytes=0-1,":   4, // 末尾空元素
		"bytes=0":      1, // 缺少 '-'
		"bytes=0--1":   2, // 第二个 '-' 落进整数
		"bytes=a-1":    0, // 非数字
		"bytes=5-2":    2, // end < start
		"bytes=-":      0, // 两侧皆空
	}
	for hdr, wantOff := range cases {
		_, err := rangespec.Parse(hdr)
		var se *rangespec.SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("Parse(%q): want *SyntaxError, got %v", hdr, err)
			continue
		}
		if se.Offset != wantOff {
			t.Errorf("Parse(%q): offset=%d, want %d", hdr, se.Offset, wantOff)
		}
	}
}

func TestSyntaxVsUnsatisfiableAreDistinct(t *testing.T) {
	// bytes=-0 语法合法，错误必须来自归一化阶段而非解析阶段。
	specs, err := rangespec.Parse("bytes=-0")
	if err != nil {
		t.Fatalf("bytes=-0 must parse, got syntax error: %v", err)
	}
	_, err = coalesce.Normalize(specs, 100)
	var ue *coalesce.UnsatisfiableError
	if !errors.As(err, &ue) {
		t.Fatalf("want *UnsatisfiableError, got %v", err)
	}
	if ue.TotalLength != 100 {
		t.Errorf("total length = %d, want 100", ue.TotalLength)
	}

	// 语法错误绝不可被当成不可满足。
	_, err = rangespec.Parse("garbage")
	var se *rangespec.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %v", err)
	}
	var ue2 *coalesce.UnsatisfiableError
	if errors.As(err, &ue2) {
		t.Fatal("syntax error must not satisfy UnsatisfiableError")
	}
}
