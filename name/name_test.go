package name

import (
	"errors"
	"testing"
)

func TestValid(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"", true},              // 空串合法
		{"a", true},             // 普通名字
		{"a/b/c", true},         // 路径分隔符按普通字符
		{`a\b`, true},           // 反斜杠同理
		{"a\x00b", false},       // 含 NUL 非法
		{"\x00", false},         // 纯 NUL 非法
		{"#rename-tmp-0", true}, // 临时名同形名字本身合法
	}
	for _, c := range cases {
		if got := Valid(c.name); got != c.valid {
			t.Errorf("Valid(%q) = %v, want %v", c.name, got, c.valid)
		}
	}
}

func TestSpaceOps(t *testing.T) {
	s, err := New("a", "", "x/y")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := New("a\x00"); !errors.Is(err, ErrIllegalName) {
		t.Fatalf("New with NUL: %v", err)
	}
	for _, n := range []string{"a", "", "x/y"} {
		if !s.Has(n) {
			t.Errorf("Has(%q) = false", n)
		}
	}
	if s.Has("b") {
		t.Error("Has(b) = true")
	}
	if err := s.Add("b"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add("c\x00"); !errors.Is(err, ErrIllegalName) {
		t.Fatalf("Add NUL: %v", err)
	}
	s.Lock()
	defer s.Unlock()
	if err := s.RenameLocked("b", "a"); err == nil {
		t.Error("RenameLocked onto existing name: want overwrite error")
	}
	if err := s.RenameLocked("zz", "w"); err == nil {
		t.Error("RenameLocked missing source: want error")
	}
	if err := s.RenameLocked("b", "c"); err != nil {
		t.Fatalf("RenameLocked: %v", err)
	}
	if s.HasLocked("b") || !s.HasLocked("c") {
		t.Error("RenameLocked did not move the name")
	}
}

func TestSnapshotEqual(t *testing.T) {
	a := Must("x", "y", "z")
	b := Must("z", "y", "x")
	if !a.Equal(b) {
		t.Error("Equal: order-independent compare failed")
	}
	got := a.Snapshot()
	want := []string{"x", "y", "z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Snapshot[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	b.Add("w")
	if a.Equal(b) {
		t.Error("Equal: different sets reported equal")
	}
}
