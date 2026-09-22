package hexline

import (
	"errors"
	"testing"
)

func TestParseSizes(t *testing.T) {
	cases := []struct {
		line string
		want uint64
	}{
		{"0", 0},
		{"5", 5},
		{"007", 7},          // leading zeros allowed
		{"aF", 0xaf},        // case insensitive
		{"AF", 0xaf},        // case insensitive
		{"ff", 255},
		{"5;a=1", 5},        // extension ignored
		{"5;a=1;b=2;c", 5},  // multiple extensions
		{"5;a", 5},          // extension without value
		{`5;a="x;y=z"`, 5},  // ';' and '=' inside quotes are data
		{`5;a="x\"y";b=2`, 5}, // escaped quote is not the end
		{`5;a="";b=tok`, 5}, // empty quoted value
		{`5;a="\\"`, 5},     // escaped backslash then close
	}
	for _, c := range cases {
		got, err := Parse([]byte(c.line))
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", c.line, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		line  string
		want  error
		index int
	}{
		{"", ErrBadSize, 0},
		{"xyz", ErrBadSize, 0},
		{"1Z", ErrBadSize, 1},
		{"5 extra", ErrBadSize, 1},       // junk after size
		{"5;a=\"x\"y", ErrBadSize, 7},    // junk after quoted-string
		{`5;a="unterminated`, ErrUnclosedQuote, 4},
		{`5;a="trailing\`, ErrUnclosedQuote, 4}, // backslash at end
		{"fffffffffffffffff", ErrSizeOverflow, 16},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.line))
		if err == nil {
			t.Errorf("Parse(%q): expected error, got none", c.line)
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("Parse(%q): error %v does not match %v", c.line, err, c.want)
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q): error is not a *ParseError", c.line)
			continue
		}
		if pe.Index != c.index {
			t.Errorf("Parse(%q): index = %d, want %d", c.line, pe.Index, c.index)
		}
	}
}

func TestQuotedExtensionWithSeparators(t *testing.T) {
	// Semicolons, equals signs and escaped quotes inside the quoted
	// value must not be treated as delimiters; a second extension
	// follows the closing quote.
	line := `1f;note="a;b=c\"d";flag=on`
	size, err := Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	if size != 0x1f {
		t.Fatalf("Parse(%q) = %d, want 31", line, size)
	}
}
