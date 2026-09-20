package headers

import (
	"reflect"
	"testing"
)

// 语义 2：键名大小写不敏感，Names 输出规范化。
func TestCanonicalNames(t *testing.T) {
	s, err := Parse("content-type: text/html\r\nX-REQUEST-ID: abc\r\n", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"Content-Type", "X-Request-Id"}
	if got := s.Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}
	for _, name := range []string{"content-type", "CONTENT-TYPE", "Content-Type"} {
		if v, ok := s.Get(name); !ok || v != "text/html" {
			t.Errorf("Get(%q) = %q, %v", name, v, ok)
		}
	}
}

// 语义 4：多值头按出现顺序，Get 用 ", " 拼接，值内逗号不拆分。
func TestMultiValue(t *testing.T) {
	raw := "Accept: text/html\r\nX-Tag: a,b\r\nAccept: application/json\r\nX-Tag: c\r\n"
	s, err := Parse(raw, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := s.Values("accept"); !reflect.DeepEqual(got, []string{"text/html", "application/json"}) {
		t.Errorf("Values(Accept) = %v", got)
	}
	if v, _ := s.Get("Accept"); v != "text/html, application/json" {
		t.Errorf("Get(Accept) = %q", v)
	}
	if got := s.Values("X-Tag"); !reflect.DeepEqual(got, []string{"a,b", "c"}) {
		t.Errorf("Values(X-Tag) = %v, comma value must stay intact", got)
	}
	if got := s.Names(); !reflect.DeepEqual(got, []string{"Accept", "X-Tag"}) {
		t.Errorf("Names = %v, want first-appearance order", got)
	}
}

// Names/Values 返回副本，修改不影响集合。
func TestAccessorsReturnCopies(t *testing.T) {
	s, err := Parse("A: 1\r\n", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s.Names()[0] = "mutated"
	s.Values("A")[0] = "mutated"
	if got := s.Names()[0]; got != "A" {
		t.Errorf("Names not a copy: %q", got)
	}
	if v, _ := s.Get("A"); v != "1" {
		t.Errorf("Values not a copy: %q", v)
	}
}
