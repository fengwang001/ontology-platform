package ptr

import (
	"errors"
	"fmt"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct{ raw, enc string }{
		{"a/b", "a~1b"}, {"~1", "~01"}, {"m~n", "m~0n"}, {"~/", "~0~1"}, {"plain", "plain"},
	}
	for _, c := range cases {
		if got := Encode(c.raw); got != c.enc {
			t.Errorf("Encode(%q)=%q want %q", c.raw, got, c.enc)
		}
		segs, err := Split("/" + c.enc)
		if err != nil || len(segs) != 1 || segs[0] != c.raw {
			t.Errorf("Split(/%s)=%v,%v want [%q]", c.enc, segs, err, c.raw)
		}
	}
}

func TestSplitInvalid(t *testing.T) {
	for _, p := range []string{"abc", "/a~", "/a~2", "/~3b"} {
		if _, err := Split(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Split(%q) err=%v want ErrInvalidPath", p, err)
		}
	}
	segs, err := Split("")
	if err != nil || segs != nil {
		t.Errorf("Split(\"\")=%v,%v want nil,nil", segs, err)
	}
	segs, err = Split("/a/b~1c")
	if err != nil || len(segs) != 2 || segs[0] != "a" || segs[1] != "b/c" {
		t.Errorf("Split multi=%v,%v", segs, err)
	}
}

func TestParseIndex(t *testing.T) {
	valid := map[string]int{"0": 0, "1": 1, "9": 9, "10": 10, "123": 123}
	for tok, want := range valid {
		if got, err := ParseIndex(tok); err != nil || got != want {
			t.Errorf("ParseIndex(%q)=%d,%v want %d", tok, got, err, want)
		}
	}
	for _, tok := range []string{"", "01", "00", "-1", "+1", "1.0", "a", "-"} {
		if _, err := ParseIndex(tok); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ParseIndex(%q) err=%v want ErrInvalidPath", tok, err)
		}
	}
}

// TestLookupCount 钉住第四节：定位检查个数不随对象键数 m 增长（按键直接查找）。
func TestLookupCount(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		doc := map[string]any{}
		for i := 0; i < m; i++ {
			doc[fmt.Sprintf("k%06d", i)] = float64(i)
		}
		before := checks.Load()
		if _, err := Step(doc, "k000042"); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		got := checks.Load() - before
		if got > 2 { // 路径段数 1 + 小常数 1，与 m 无关
			t.Errorf("m=%d: checks=%d 随 m 增长", m, got)
		}
	}
}
