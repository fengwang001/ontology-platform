package headers_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/headers"
)

// 注入防御是硬要求：所有变体必须可判定地拒绝，回写字节不得含额外头部。
func TestInjectionRejected(t *testing.T) {
	variants := []struct {
		name  string
		value string
	}{
		{"CRLF 注入", "a\r\nEvil: 1"},
		{"裸 LF", "a\nEvil: 1"},
		{"裸 CR", "a\rEvil: 1"},
		{"NUL", "a\x00Evil"},
		{"百分号编码 CRLF", "a%0d%0aEvil: 1"},
		{"百分号大写 LF", "a%0AEvil: 1"},
		{"百分号 NUL", "a%00Evil"},
		{"其他控制字符", "a\x1b[0mEvil"},
		{"DEL", "a\x7fEvil"},
	}
	for _, v := range variants {
		s := headers.New(nil)
		if err := s.Add("X-Test", v.value); !errors.Is(err, headers.ErrForbiddenByte) {
			t.Errorf("%s: Add 未拒绝, err = %v", v.name, err)
		}
		if err := s.Set("X-Test", v.value); !errors.Is(err, headers.ErrForbiddenByte) {
			t.Errorf("%s: Set 未拒绝, err = %v", v.name, err)
		}
		if s.Len() != 0 {
			t.Errorf("%s: 拒绝后状态发生变化, Len = %d", v.name, s.Len())
		}
		if out := s.Bytes(); bytes.Contains(out, []byte("Evil")) {
			t.Errorf("%s: 回写字节含注入内容: %q", v.name, out)
		}
	}
}

// 拒绝不得影响集合中已有的正常头部。
func TestRejectionKeepsState(t *testing.T) {
	s := headers.New(nil)
	if err := s.Add("X-Good", "clean"); err != nil {
		t.Fatal(err)
	}
	before := s.Bytes()
	if err := s.Set("X-Good", "a\r\nEvil: 1"); err == nil {
		t.Fatal("Set 应拒绝注入值")
	}
	if !bytes.Equal(s.Bytes(), before) {
		t.Errorf("拒绝后回写发生变化: %q != %q", s.Bytes(), before)
	}
	if got := s.GetAll("X-Good"); len(got) != 1 || got[0] != "clean" {
		t.Errorf("已有值被污染: %v", got)
	}
}

// 解析路径同样拦截：字节流里的控制字符与编码绕过一律判错。
func TestParseRejectsInjection(t *testing.T) {
	inputs := []struct {
		name string
		in   string
	}{
		{"值中 NUL", "X-A: a\x00b\r\n\r\n"},
		{"值中控制字符", "X-A: a\x07b\r\n\r\n"},
		{"值中百分号 CRLF", "X-A: a%0d%0aEvil:%201\r\n\r\n"},
		{"非法名字", "X A: 1\r\n\r\n"},
		{"冒号前空白", "X-A : 1\r\n\r\n"},
		{"冒号前制表符", "X-A\t: 1\r\n\r\n"},
	}
	for _, c := range inputs {
		s, err := headers.Parse([]byte(c.in), nil)
		if err == nil {
			t.Errorf("%s: 应判错却成功, 集合 = %q", c.name, s.Bytes())
		}
		if s != nil {
			t.Errorf("%s: 失败不应返回半截集合", c.name)
		}
	}
}
