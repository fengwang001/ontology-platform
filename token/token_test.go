package token

import "testing"

func TestValidTenant(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", false}, {"a", true}, {"t-a_1", true},
		{string(make([]byte, 65)), false},
		{string(make([]byte, 64)), true},
	} {
		if got := ValidTenant(tc.in); got != tc.ok {
			t.Fatalf("ValidTenant(len=%d)=%v want %v", len(tc.in), got, tc.ok)
		}
	}
}

func TestTokenizeMask(t *testing.T) {
	tok, err := Tokenize("open 7 files v2 ok")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"open", Wildcard, "files", Wildcard, "ok"}
	if len(tok) != len(want) {
		t.Fatalf("got %v want %v", tok, want)
	}
	for i := range want {
		if tok[i] != want[i] {
			t.Fatalf("pos %d: %q != %q (全量=%v)", i, tok[i], want[i], tok)
		}
	}
	t.Logf("输入 %q => 掩码 %v，判定: 含数字词→<*>", "open 7 files v2 ok", tok)
}

func TestTokenizeInvalid(t *testing.T) {
	bad := []string{
		"", " a", "a ", "a  b", " ",
		repeat("w", 65),
		repeat("w ", 17), // 17 个词
	}
	for _, msg := range bad {
		if _, err := Tokenize(msg); err == nil {
			t.Fatalf("期望非法但被接受: %q", msg)
		}
	}
	good := []string{"a", repeat("w", 64), joinN(32)}
	for _, msg := range good {
		if _, err := Tokenize(msg); err != nil {
			t.Fatalf("期望合法但被拒: %q: %v", msg, err)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func joinN(n int) string {
	out := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, 'w')
	}
	return string(out)
}
