package headers

import (
	"bytes"
	"errors"
	"testing"

	"ontology/token"
)

// TestInjectionRejected 五类注入变体（含编码绕过）全部被拒，回写无额外头部，状态零变化。
func TestInjectionRejected(t *testing.T) {
	variants := []struct {
		name string
		v    string
	}{
		{"CRLF", "a\r\nEvil: 1"},
		{"LF", "a\nEvil: 1"},
		{"CR", "a\rEvil: 1"},
		{"NUL", "a\x00b"},
		{"控制字符", "a\x1fb"},
		{"百分号编码", "a%0d%0aEvil:%201"},
		{"反斜杠转义", `a\r\nEvil: 1`},
		{"十六进制转义", `a\x0aEvil: 1`},
		{"unicode转义", "a" + string([]byte{'\\', 'u', '0', '0', '0', 'a'}) + "Evil"},
	}
	for _, c := range variants {
		s := New(testRegistry(), Config{})
		if err := s.Set("X-Safe", "clean"); err != nil {
			t.Fatal(err)
		}
		before := s.Marshal()
		if err := s.Set("X-Safe", c.v); err == nil {
			t.Errorf("%s: Set 未拒绝 %q", c.name, c.v)
		}
		if err := s.Add("X-Other", c.v); err == nil {
			t.Errorf("%s: Add 未拒绝 %q", c.name, c.v)
		}
		out := s.Marshal()
		if bytes.Contains(out, []byte("Evil")) {
			t.Errorf("%s: 回写字节含注入头部", c.name)
		}
		if !bytes.Equal(out, before) {
			t.Errorf("%s: 拒绝后状态被改变", c.name)
		}
		if got, _ := s.Get("X-Safe"); got != "clean" {
			t.Errorf("%s: 原值被篡改: %q", c.name, got)
		}
	}
	// 名字通道同样无注入面。
	s := New(nil, Config{})
	if err := s.Set("X\r\nEvil", "1"); !errors.Is(err, token.ErrIllegalName) {
		t.Errorf("名字注入未拒绝: %v", err)
	}
}
