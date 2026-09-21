package editdist

import (
	"errors"
	"testing"
)

func TestRuneSemanticsCafe(t *testing.T) {
	// "café"（é 为单码点 U+00E9，两字节）与 "cafe" 的距离按码点算是 1，
	// 按字节算会是 2。
	r, err := Distance("café", "cafe", 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Exceeded || r.Distance != 1 {
		t.Fatalf("café vs cafe = %+v, want distance 1", r)
	}
}

func TestRuneSemanticsEmoji(t *testing.T) {
	// 🙂 是四字节单码点：增删一个表情符号计距离 1。
	ins, err := Distance("a🙂b", "ab", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ins.Exceeded || ins.Distance != 1 {
		t.Fatalf("emoji insert = %+v, want distance 1", ins)
	}
	del, err := Distance("🎉🙂🎊", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if del.Exceeded || del.Distance != 3 {
		t.Fatalf("emoji delete = %+v, want distance 3", del)
	}
	sub, err := Distance("🙂", "🎉", 1)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Exceeded || sub.Distance != 1 {
		t.Fatalf("emoji substitute = %+v, want distance 1", sub)
	}
}

func TestRuneLen(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"café", 4},
		{"🙂🎉x", 3},
		{"日本語", 3},
	}
	for _, tc := range cases {
		got, err := RuneLen(tc.s)
		if err != nil {
			t.Fatalf("RuneLen(%q): %v", tc.s, err)
		}
		if got != tc.want {
			t.Errorf("RuneLen(%q)=%d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestInvalidUTF8(t *testing.T) {
	badA := string([]byte{'a', 'b', 0xff, 'c'})
	_, err := Distance(badA, "abc", 3)
	var uerr *UTF8Error
	if !errors.As(err, &uerr) {
		t.Fatalf("want *UTF8Error, got %v", err)
	}
	if uerr.Side != "a" || uerr.Offset != 2 {
		t.Fatalf("side=%q offset=%d, want a@2", uerr.Side, uerr.Offset)
	}

	badB := string([]byte{'x', 0x80, 'y'})
	_, err = Distance("ok", badB, 3)
	uerr = nil
	if !errors.As(err, &uerr) {
		t.Fatalf("want *UTF8Error, got %v", err)
	}
	if uerr.Side != "b" || uerr.Offset != 1 {
		t.Fatalf("side=%q offset=%d, want b@1", uerr.Side, uerr.Offset)
	}

	if _, err = RuneLen(badA); err == nil {
		t.Fatal("RuneLen should reject invalid UTF-8")
	}
	// 合法编码的 U+FFFD 本身不是错误。
	r, err := Distance("�", "�", 0)
	if err != nil || r.Exceeded || r.Distance != 0 {
		t.Fatalf("literal U+FFFD must be accepted: %+v err=%v", r, err)
	}
}
