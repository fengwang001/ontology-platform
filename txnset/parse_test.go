package txnset

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single point", "src:1", "src:1"},
		{"closed interval", "src:1-5", "src:1-5"},
		{"zero point", "src:0", "src:0"},
		{"multiple intervals", "src:1,3,5-9", "src:1,3,5-9"},
		{"unordered merged", "src:9,1-2,4,3,2", "src:1-4,9"},
		{"adjacent merged", "src:1-2,3-4", "src:1-4"},
		{"multiple sources sorted",
			"zeta:1;alpha:2;mid:3-5",
			"alpha:2;mid:3-5;zeta:1"},
		{"same source split across entries merged",
			"a:1-3;a:2-9;a:100",
			"a:1-9,100"},
		{"identifier chars", "a-b_c1:7", "a-b_c1:7"},
		{"max uint64", "s:18446744073709551615", "s:18446744073709551615"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if got := s.Canonical(); got != tc.want {
				t.Fatalf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
		kind Kind
		pos  int
	}{
		{"space after source", "a :1", KindSyntax, 1},
		{"tab", "a:\t1", KindSyntax, 2},
		{"leading space", " a:1", KindSyntax, 0},
		{"trailing newline", "a:1\n", KindSyntax, 3},
		{"space inside number", "a:1 2", KindSyntax, 3},
		{"missing colon", "a1", KindSyntax, 2},
		{"source starts digit", "1a:1", KindIdentifier, 0},
		{"source dot", "a.b:1", KindIdentifier, 1},
		{"source slash", "a/b:1", KindIdentifier, 1},
		{"leading zero", "a:01", KindNumber, 2},
		{"leading zero interval", "a:00-5", KindNumber, 2},
		{"overflow", "a:18446744073709551616", KindNumber, 2},
		{"inverted interval", "a:9-3", KindNumber, 3},
		{"dangling dash", "a:5-", KindSyntax, 4},
		{"leading dash", "a:-5", KindSyntax, 2},
		{"trailing comma", "a:1,", KindSyntax, 4},
		{"leading comma", "a:,1", KindSyntax, 2},
		{"double comma", "a:1,,2", KindSyntax, 4},
		{"trailing semicolon", "a:1;", KindSyntax, 4},
		{"leading semicolon", ";a:1", KindSyntax, 0},
		{"double semicolon", "a:1;;b:2", KindSyntax, 4},
		{"empty interval list", "a:", KindSyntax, 2},
		{"garbage after entry", "a:1x", KindSyntax, 3},
		{"stray punctuation", "a:1;@", KindIdentifier, 4},
		{"only colon", ":1", KindSyntax, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.in)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse(%q) error %v is not *ParseError", tc.in, err)
			}
			if pe.Kind != tc.kind {
				t.Fatalf("Parse(%q) kind = %v, want %v (err: %v)", tc.in, pe.Kind, tc.kind, err)
			}
			if pe.Pos != tc.pos {
				t.Fatalf("Parse(%q) pos = %d, want %d (err: %v)", tc.in, pe.Pos, tc.pos, err)
			}
		})
	}
}

func TestParseErrorSentinels(t *testing.T) {
	checks := []struct {
		in   string
		want error
	}{
		{"a :1", ErrSyntax},
		{"a.b:1", ErrIdentifier},
		{"a:99", nil},
		{"a:1-2-3", ErrSyntax},
		{"a:18446744073709551616", ErrNumber},
	}
	for _, tc := range checks {
		_, err := Parse(tc.in)
		if tc.want == nil {
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error %v", tc.in, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("Parse(%q) err=%v, want errors.Is %v", tc.in, err, tc.want)
		}
	}
}

func TestParseReportsFirstErrorLeftToRight(t *testing.T) {
	// 分号后先是空白（第 4 字节），之后还有标识错误；必须报告最左的空白。
	_, err := Parse("a:1; b@:2")
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Pos != 4 || pe.Kind != KindSyntax {
		t.Fatalf("got %v, want syntax error at pos 4", err)
	}
}

func TestParseTooManyIntervals(t *testing.T) {
	var b strings.Builder
	b.WriteString("s:")
	for i := 0; i <= MaxIntervals; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("1")
	}
	_, err := Parse(b.String())
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("got %v, want ErrTooMany", err)
	}

	// 恰好在限制内合法。
	b.Reset()
	b.WriteString("s:")
	for i := 0; i < MaxIntervals; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("1")
	}
	if _, err := Parse(b.String()); err != nil {
		t.Fatalf("boundary parse failed: %v", err)
	}
}

func TestParseIsPure(t *testing.T) {
	// 解析非法文本不影响任何已有集合（Parse 本身不接收集合）。
	s, _ := Parse("a:1-3")
	before := s.Canonical()
	if _, err := Parse("a:1;broken@@"); err == nil {
		t.Fatal("expected parse error")
	}
	if got := s.Canonical(); got != before {
		t.Fatalf("existing set changed: %q != %q", got, before)
	}
}
