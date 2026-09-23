package headers

import (
	"errors"
	"strings"
	"testing"

	"ontology/token"
)

// TestParseSyntaxErrors 表驱动：各类语法错误都必须可判定（阶段 + 哨兵错误）。
func TestParseSyntaxErrors(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		stage Stage
		sent  error // 可为 nil（纯截断类错误）
	}{
		{"首行续行", " x\r\nA: 1\r\n\r\n", StageFold, nil},
		{"首行tab续行", "\tx\r\n\r\n", StageFold, nil},
		{"冒号前空格", "X : 1\r\n\r\n", StageColon, ErrNameColonSpace},
		{"冒号前tab", "X\t: 1\r\n\r\n", StageColon, ErrNameColonSpace},
		{"空名字", ": 1\r\n\r\n", StageName, token.ErrIllegalName},
		{"名字含非法字符", "X@Y: 1\r\n\r\n", StageName, token.ErrIllegalName},
		{"缺冒号", "X\r\n\r\n", StageColon, nil},
		{"值含裸CR", "X: a\rb\r\n\r\n", StageValue, token.ErrIllegalValue},
		{"值含NUL", "X: a\x00b\r\n\r\n", StageValue, token.ErrIllegalValue},
		{"值含编码注入", "X: a%0d%0aB:1\r\n\r\n", StageValue, token.ErrEncodedInjection},
		{"终止行后有数据", "A: 1\r\n\r\nB: 2\r\n", StageEnd, nil},
		{"缺终止空行", "A: 1\r\n", StageEnd, nil},
		{"空输入", "", StageEnd, nil},
	}
	for _, c := range cases {
		s, err := Parse([]byte(c.in), nil, Config{})
		if s != nil {
			t.Errorf("%s: 错误输入不应返回集合", c.name)
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: 错误类型 %T 不是 ParseError", c.name, err)
			continue
		}
		if pe.Stage != c.stage {
			t.Errorf("%s: stage = %v, want %v", c.name, pe.Stage, c.stage)
		}
		if c.sent != nil && !errors.Is(err, c.sent) {
			t.Errorf("%s: err = %v, want sentinel %v", c.name, err, c.sent)
		}
	}
}

// TestTruncationEveryByte 遍历所有截断点：必须返回可判定错误，不 panic、无半截集合。
func TestTruncationEveryByte(t *testing.T) {
	inputs := []string{
		"A: 1\r\n\r\n",
		"A: 1\r\nLong-Name: some value\r\n\tcontinued here\r\nB: 2\r\n\r\n",
		"X: \r\n\r\n",
	}
	for _, full := range inputs {
		if _, err := Parse([]byte(full), nil, Config{}); err != nil {
			t.Fatalf("完整输入 %q 应成功: %v", full, err)
		}
		for i := 0; i < len(full); i++ {
			s, err := Parse([]byte(full[:i]), nil, Config{})
			if s != nil {
				t.Fatalf("%q 截断到 %d 返回了半截集合", full, i)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("%q 截断到 %d: 错误 %v 不可判定", full, i, err)
			}
			if pe.Stage < StageName || pe.Stage > StageEnd {
				t.Fatalf("%q 截断到 %d: 非法阶段 %v", full, i, pe.Stage)
			}
		}
	}
}

// TestLimits 四类超限：彼此可判定，且拒绝后集合状态零变化。
func TestLimits(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		in   string
		want error
	}{
		{"条数超限", Config{MaxHeaders: 1}, "A: 1\r\nB: 2\r\n\r\n", ErrTooManyHeaders},
		{"名字超限", Config{MaxName: 3}, "Abcd: 1\r\n\r\n", ErrNameTooLong},
		{"值超限", Config{MaxValue: 3}, "A: 1234\r\n\r\n", ErrValueTooLong},
		{"总字节超限", Config{MaxBytes: 8}, "Attack: 123\r\n\r\n", ErrInputTooLarge},
	}
	for _, c := range cases {
		s := New(nil, c.cfg)
		if err := s.Parse([]byte("A:\r\n\r\n")); err != nil {
			t.Fatalf("%s: 预置解析失败: %v", c.name, err)
		}
		before := string(s.Marshal())
		err := s.Parse([]byte(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if string(s.Marshal()) != before || s.Len() != 1 {
			t.Errorf("%s: 超限拒绝后状态被改变", c.name)
		}
	}
	// 四类错误彼此可区分。
	sents := []error{ErrTooManyHeaders, ErrNameTooLong, ErrValueTooLong, ErrInputTooLarge}
	for i, a := range sents {
		for j, b := range sents {
			if i != j && errors.Is(a, b) {
				t.Errorf("超限错误 %v 与 %v 不可区分", a, b)
			}
		}
	}
}

// TestParseNormalizes 不规范输入被规范化并置标志位。
func TestParseNormalizes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		norm bool
		want string // 期望的规范回写
	}{
		{"已规范", "A-B: x\r\nC: y\r\n\r\n", false, "A-B: x\r\nC: y\r\n\r\n"},
		{"名字大小写", "a-b: x\r\n\r\n", true, "A-B: x\r\n\r\n"},
		{"值首尾空白", "A:   x  \r\n\r\n", true, "A: x\r\n\r\n"},
		{"LF行尾", "A: x\n\n", true, "A: x\r\n\r\n"},
		{"折行展开", "A: x\r\n y\r\n\r\n", true, "A: x y\r\n\r\n"},
		{"值内部空白保留", "A: x  y\r\n\r\n", false, "A: x  y\r\n\r\n"},
		{"空值", "A:\r\n\r\n", true, "A: \r\n\r\n"},
	}
	for _, c := range cases {
		s, err := Parse([]byte(c.in), nil, Config{})
		if err != nil {
			t.Errorf("%s: 解析失败: %v", c.name, err)
			continue
		}
		if s.Normalized() != c.norm {
			t.Errorf("%s: Normalized = %v, want %v", c.name, s.Normalized(), c.norm)
		}
		if got := string(s.Marshal()); got != c.want {
			t.Errorf("%s: Marshal = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestIdempotent 规范形态再解析再回写幂等。
func TestIdempotent(t *testing.T) {
	messy := []string{
		"a-b:  x \r\nC: y\n\r\n",
		"A: one\r\n two\r\n\tthree\r\nB: z\r\n\r\n",
		"X: a,b\r\nx: c\r\n\r\n",
	}
	for _, in := range messy {
		s1, err := Parse([]byte(in), nil, Config{})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		m1 := s1.Marshal()
		s2, err := Parse(m1, nil, Config{})
		if err != nil {
			t.Fatalf("重解析 %q: %v", m1, err)
		}
		if s2.Normalized() {
			t.Errorf("%q 规范形态仍被标记改写", in)
		}
		if string(s2.Marshal()) != string(m1) {
			t.Errorf("%q 不幂等: %q vs %q", in, m1, s2.Marshal())
		}
	}
}

func TestParseErrorMessage(t *testing.T) {
	_, err := Parse([]byte("X : 1\r\n\r\n"), nil, Config{})
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("不是 ParseError: %v", err)
	}
	msg := pe.Error()
	if !strings.Contains(msg, "colon") || !strings.Contains(msg, "header 0") {
		t.Errorf("错误信息缺少头部序号或阶段: %q", msg)
	}
}
