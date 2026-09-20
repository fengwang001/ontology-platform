package headers

import (
	"errors"
	"reflect"
	"testing"
)

// 语义 1：行格式 —— 冒号后空白修剪，值内部空白保留，名字与冒号间不允许空格。
func TestLineFormat(t *testing.T) {
	s, err := Parse("Host:   example.com  \r\nX-A:  a  b \t c\r\n", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := s.Get("Host"); v != "example.com" {
		t.Errorf("Host = %q, want %q", v, "example.com")
	}
	if v, _ := s.Get("X-A"); v != "a  b \t c" {
		t.Errorf("X-A = %q, want inner whitespace kept", v)
	}
	if _, err := Parse("Host : v\r\n", nil); !errors.Is(err, ErrMalformed) {
		t.Errorf("space before colon: err = %v, want ErrMalformed", err)
	}
}

// 语义 3：折行续行 —— 前导空白压成一个空格，去掉上一行尾随空白。
func TestObsFold(t *testing.T) {
	s, err := Parse("X-Fold: hello  \r\n \t world\t\r\n\tagain\r\n", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v, _ := s.Get("X-Fold"); v != "hello world again" {
		t.Errorf("X-Fold = %q, want %q", v, "hello world again")
	}
	if _, err := Parse(" orphan\r\nHost: a\r\n", nil); !errors.Is(err, ErrMalformed) {
		t.Errorf("leading continuation: err = %v, want ErrMalformed", err)
	}
}

// 语义 5：单值头冲突 —— 相同去重，不同报 ErrSingleValue。
func TestSingleValue(t *testing.T) {
	s, err := Parse("Host: a\r\nHost: a\r\nX-M: 1\r\nX-M: 2\r\n", []string{"host"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := s.Values("Host"); len(got) != 1 || got[0] != "a" {
		t.Errorf("Host values = %v, want [a]", got)
	}
	if _, err := Parse("Host: a\r\nHost: b\r\n", []string{"HOST"}); !errors.Is(err, ErrSingleValue) {
		t.Errorf("conflict: err = %v, want ErrSingleValue", err)
	}
	// 判断相同时大小写敏感
	if _, err := Parse("Host: a\r\nHost: A\r\n", []string{"Host"}); !errors.Is(err, ErrSingleValue) {
		t.Errorf("case-sensitive conflict: err = %v, want ErrSingleValue", err)
	}
}

// 语义 6：空值合法且可与不存在区分。
func TestEmptyValue(t *testing.T) {
	s, err := Parse("X-Empty:\r\n", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	v, ok := s.Get("X-Empty")
	if !ok || v != "" {
		t.Errorf("Get(X-Empty) = %q, %v; want \"\", true", v, ok)
	}
	if _, ok := s.Get("X-Missing"); ok {
		t.Error("Get(X-Missing) ok = true, want false")
	}
	if got := s.Values("X-Empty"); len(got) != 1 || got[0] != "" {
		t.Errorf("Values(X-Empty) = %v, want [\"\"]", got)
	}
	if got := s.Names(); !reflect.DeepEqual(got, []string{"X-Empty"}) {
		t.Errorf("Names = %v, want [X-Empty]", got)
	}
}

// 语义 7：各类语法错误均返回 ErrMalformed 且 Set 为 nil。
func TestMalformed(t *testing.T) {
	cases := map[string]string{
		"missing colon":   "NoColonHere\r\n",
		"empty name":      ": v\r\n",
		"space in name":   "Bad Name: v\r\n",
		"tab in name":     "Bad\tName: v\r\n",
		"ctrl in name":    "Bad\x01Name: v\r\n",
		"leading fold":    " folded\r\n",
		"lf only":         "Host: a\n",
		"cr only":         "Host: a\r",
		"no terminator":   "Host: a",
		"bare cr in line": "Host: a\rb\r\n",
	}
	for desc, raw := range cases {
		s, err := Parse(raw, nil)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", desc, err)
		}
		if s != nil {
			t.Errorf("%s: Set = %v, want nil", desc, s)
		}
	}
}

// 语义 8：不依赖入参底层数组，且重复解析结果一致。
func TestRepeatableAndIndependent(t *testing.T) {
	raw := "Host: a\r\nX-M: 1\r\nX-M: 2\r\n"
	s1, err := Parse(raw, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s2, err := Parse(raw, nil)
	if err != nil {
		t.Fatalf("Parse again: %v", err)
	}
	if !reflect.DeepEqual(s1.Names(), s2.Names()) ||
		!reflect.DeepEqual(s1.Values("X-M"), s2.Values("X-M")) {
		t.Error("two parses of same input differ")
	}
	// 修改 s2 的返回值不得影响 s1（Values 返回副本）
	s2.Values("X-M")[0] = "mutated"
	if got := s1.Values("X-M")[0]; got != "1" {
		t.Errorf("s1 affected by s2 mutation: %q", got)
	}
}
