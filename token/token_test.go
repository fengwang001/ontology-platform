package token

import (
	"errors"
	"testing"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"Accept", true},
		{"x-custom-1", true},
		{"!#$%&'*+-.^_`|~", true},
		{"", false},
		{"X Y", false},    // 空格不是 tchar
		{"X:Y", false},    // 冒号不是 tchar
		{"X\tY", false},   // 制表符不是 tchar
		{"X\rY", false},   // CR 不是 tchar
		{"X\nY", false},   // LF 不是 tchar
		{"Xé", false},     // 非 ASCII 不是 tchar
		{"X\x00Y", false}, // NUL 不是 tchar
	}
	for _, c := range cases {
		if got := ValidName(c.name); got != c.ok {
			t.Errorf("ValidName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestValidateValue(t *testing.T) {
	cases := []struct {
		name string
		v    string
		want error
	}{
		{"普通文本", "hello world", nil},
		{"空值", "", nil},
		{"制表符允许", "a\tb", nil},
		{"全部可见字符", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", nil},
		{"CRLF 注入", "a\r\nEvil: 1", ErrIllegalValue},
		{"LF 注入", "a\nEvil: 1", ErrIllegalValue},
		{"裸 CR 注入", "a\rEvil: 1", ErrIllegalValue},
		{"NUL", "a\x00b", ErrIllegalValue},
		{"其他控制字符", "a\x01b", ErrIllegalValue},
		{"DEL", "a\x7fb", ErrIllegalValue},
		{"非 ASCII", "aéb", ErrIllegalValue},
		{"百分号编码 CRLF", "a%0d%0aEvil:%201", ErrEncodedInjection},
		{"百分号编码 NUL", "a%00b", ErrEncodedInjection},
		{"反斜杠转义 r n", `a\r\nEvil: 1`, ErrEncodedInjection},
		{"反斜杠十六进制", `a\x0dEvil`, ErrEncodedInjection},
		{"反斜杠 unicode", "a" + string([]byte{'\\', 'u', '0', '0', '0', 'd'}) + "Evil", ErrEncodedInjection},
		{"反斜杠 NUL", `a\0b`, ErrEncodedInjection},
		{"无害百分号", "100% sure", nil},
		{"合法百分号编码", "%41%42", nil}, // 解码为可见字符，不拦截
		{"反斜杠普通字符", `a\qb`, nil},
	}
	for _, c := range cases {
		err := ValidateValue(c.v)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: ValidateValue(%q) = %v, want %v", c.name, c.v, err, c.want)
		}
	}
}

func TestIsValueByteBoundaries(t *testing.T) {
	cases := []struct {
		b  byte
		ok bool
	}{
		{0x00, false}, {0x08, false}, {0x09, true}, {0x0a, false},
		{0x0d, false}, {0x1f, false}, {0x20, true}, {0x7e, true},
		{0x7f, false}, {0x80, false}, {0xff, false},
	}
	for _, c := range cases {
		if got := IsValueByte(c.b); got != c.ok {
			t.Errorf("IsValueByte(%#x) = %v, want %v", c.b, got, c.ok)
		}
	}
}
