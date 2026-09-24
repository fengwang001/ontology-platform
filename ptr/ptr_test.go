package ptr

import (
	"errors"
	"fmt"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct{ tok, enc string }{
		{"", ""}, {"a", "a"}, {"a/b", "a~1b"}, {"m~n", "m~0n"},
		{"~1", "~01"}, {"~0", "~00"}, {"~/", "~0~1"}, {"a~1b/c~0d", "a~01b~1c~00d"},
	}
	for _, c := range cases {
		if got := Encode(c.tok); got != c.enc {
			t.Errorf("Encode(%q)=%q want %q", c.tok, got, c.enc)
		}
		got, err := Decode(c.enc)
		if err != nil || got != c.tok {
			t.Errorf("Decode(%q)=%q,%v want %q", c.enc, got, err, c.tok)
		}
	}
	for _, bad := range []string{"~", "~2", "a~", "x~9y", "~~"} {
		if _, err := Decode(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Decode(%q) err=%v want ErrInvalidPath", bad, err)
		}
	}
}

func TestSplitAndIndex(t *testing.T) {
	if segs, err := Split(""); err != nil || segs != nil {
		t.Errorf("Split(\"\")=%v,%v", segs, err)
	}
	segs, err := Split("/a~1b/~01/x")
	if err != nil || len(segs) != 3 || segs[0] != "a/b" || segs[1] != "~1" || segs[2] != "x" {
		t.Errorf("Split=%v,%v", segs, err)
	}
	for _, bad := range []string{"x", "a/b", "/~3", "/~"} {
		if _, err := Split(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Split(%q) err=%v", bad, err)
		}
	}
	okIdx := map[string]int{"0": 0, "1": 1, "10": 10, "999": 999}
	for s, want := range okIdx {
		if n, err := ParseIndex(s); err != nil || n != want {
			t.Errorf("ParseIndex(%q)=%d,%v", s, n, err)
		}
	}
	for _, bad := range []string{"", "01", "-1", "-", "1a", " 1", "007"} {
		if _, err := ParseIndex(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ParseIndex(%q) err=%v", bad, err)
		}
	}
}

// 定位按键直接查找：检查个数不随顶层键数 m 线性增长。
func TestLocateCountIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		doc := map[string]any{}
		for i := 0; i < m; i++ {
			doc[fmt.Sprintf("k%05d", i)] = float64(i)
		}
		segs, err := Split("/k" + fmt.Sprintf("%05d", m/2))
		if err != nil {
			t.Fatal(err)
		}
		checks.Store(0)
		parent, last, _, err := Locate(doc, segs)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := parent.(map[string]any)[last]; !ok {
			t.Fatalf("m=%d: target missing", m)
		}
		if got := checks.Load(); got > int64(len(segs)+1) {
			t.Errorf("m=%d: checks=%d grows with m (limit %d)", m, got, len(segs)+1)
		}
	}
}

func TestLocateErrors(t *testing.T) {
	doc := map[string]any{"a": map[string]any{"b": []any{1.0, 2.0}}}
	if _, _, _, err := Locate(doc, []string{"zz", "a"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key err=%v", err)
	}
	if _, _, _, err := Locate(doc, []string{"a", "b", "01", "x"}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("bad index err=%v", err)
	}
	if _, _, _, err := Locate(doc, []string{"a", "b", "5", "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("oob index err=%v", err)
	}
	if _, _, _, err := Locate(doc, nil); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("empty segs err=%v", err)
	}
}
