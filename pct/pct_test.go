package pct

import (
	"strings"
	"testing"
)

func safePathByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.ContainsRune("-._~!$&'()*+,;=:@", rune(b))
}

func TestNormalizeUpperAndRedundant(t *testing.T) {
	cases := []struct{ in, want string }{
		{"%41", "A"},
		{"%41%42%63", "ABc"},
		{"%2f", "%2F"},
		{"a%2fb", "a%2Fb"},
		{"%e4%b8%ad", "%E4%B8%AD"},
		{"A%20B", "A%20B"},
	}
	for _, c := range cases {
		got, err := Normalize(c.in, safePathByte, nil)
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q)=%q,%v want %q", c.in, got, err, c.want)
		}
		again, err := Normalize(got, safePathByte, nil)
		if err != nil || again != got {
			t.Errorf("not idempotent on %q: %q err=%v", c.in, again, err)
		}
	}
}

func TestMalformedEscapes(t *testing.T) {
	cases := []struct {
		in   string
		kind string
		off  int
	}{
		{"ab%", KindTruncated, 2},
		{"%4", KindTruncated, 0},
		{"%GG", KindBadHex, 1},
		{"%4G", KindBadHex, 2},
		{"%E4%B8", KindUTF8, 0},          // truncated multibyte
		{"%FF%FF%FF", KindUTF8, 0},       // never-valid bytes
		{"ok%C3%28", KindUTF8, 2},        // 2-byte lead then ASCII (
		{"a%E4%B8%ADb%FF", KindUTF8, 11}, // bad byte after good rune
	}
	for _, c := range cases {
		got, err := Normalize(c.in, safePathByte, nil)
		if got != "" {
			t.Errorf("Normalize(%q) returned partial result %q", c.in, got)
		}
		e, ok := err.(*EscapeError)
		if !ok || e.Kind != c.kind || e.Offset != c.off {
			t.Errorf("Normalize(%q) err=%v want kind=%s off=%d", c.in, err, c.kind, c.off)
		}
	}
}

func TestErrorClassPredicates(t *testing.T) {
	if _, err := Normalize("%", nil, nil); !IsTruncated(err) || IsBadHex(err) || IsInvalidUTF8(err) {
		t.Fatal("truncated classification wrong")
	}
	if _, err := Normalize("%zz", nil, nil); !IsBadHex(err) || IsTruncated(err) {
		t.Fatal("badhex classification wrong")
	}
	if _, err := Normalize("%ff", func(b byte) bool { return b >= 0x80 }, nil); !IsInvalidUTF8(err) {
		t.Fatal("utf8 classification wrong")
	}
}

func TestScanCountSinglePass(t *testing.T) {
	var scanned int
	in := "a%2f%41" + strings.Repeat("z", 100)
	if _, err := Normalize(in, safePathByte, func(n int) { scanned += n }); err != nil {
		t.Fatal(err)
	}
	if scanned != len(in) {
		t.Fatalf("scanned %d bytes, want %d", scanned, len(in))
	}
}
