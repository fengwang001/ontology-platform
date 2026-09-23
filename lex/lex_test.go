package lex

import (
	"errors"
	"strings"
	"testing"
)

func run(s string) []Event {
	var m Machine
	var out []Event
	for i := 0; i < len(s); i++ {
		m.Step(s[i], i)
		out = append(out, m.Events()...)
	}
	out = append(out, m.End()...)
	return out
}

func TestEvents(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // literal bytes between the only Start/End
	}{
		{"single keeps backslash", `'a\b'`, `a\b`},
		{"single trailing backslash", `'a\'`, `a\`},
		{"double escaped quote", `"a\"b"`, `a"b`},
		{"double escaped backslash", `"a\\b"`, `a\b`},
		{"double keeps backslash", `"a\b"`, `a\b`},
		{"double escaped dollar", `"a\$"`, `a$`},
		{"double continuation", "\"a\\\nb\"", "ab"},
		{"unquoted escaped space", `a\ b`, `a b`},
		{"unquoted escaped backslash", `a\\`, `a\`},
		{"unquoted continuation", "a\\\nb", "ab"},
		{"concat", `a"b"'c'`, "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			starts, ends := 0, 0
			for _, ev := range run(c.input) {
				switch ev.Kind {
				case Start:
					starts++
				case End:
					ends++
				case Byte:
					b.WriteByte(ev.Byte)
				}
			}
			if starts != 1 || ends != 1 || b.String() != c.want {
				t.Fatalf("got %q (start=%d end=%d), want %q", b.String(), starts, ends, c.want)
			}
		})
	}
}

func TestEmptyWordEvents(t *testing.T) {
	cases := []struct {
		input     string
		wantWords int
		wantFirst string
	}{
		{`""''`, 1, ""},
		{`a""`, 1, "a"},
		{"\\\n", 0, ""},
		{`a '' b`, 3, "a"},
	}
	for _, c := range cases {
		events := run(c.input)
		words, cur := 0, ""
		for _, ev := range events {
			switch ev.Kind {
			case Start:
				cur = ""
			case Byte:
				cur += string(ev.Byte)
			case End:
				if words == 0 && cur != c.wantFirst {
					t.Errorf("%q first word = %q, want %q", c.input, cur, c.wantFirst)
				}
				words++
			}
		}
		if words != c.wantWords {
			t.Errorf("%q produced %d words, want %d", c.input, words, c.wantWords)
		}
	}
}

func TestEndErrors(t *testing.T) {
	cases := []struct {
		input  string
		want   error
		offset int
	}{
		{`'abc`, ErrUnclosedSingle, 0},
		{`a'bc`, ErrUnclosedSingle, 1},
		{`"abc`, ErrUnclosedDouble, 0},
		{`a"b\`, ErrUnclosedDouble, 1},
		{`abc\`, ErrTrailingBackslash, 3},
	}
	for _, c := range cases {
		var m Machine
		for i := 0; i < len(c.input); i++ {
			m.Step(c.input[i], i)
			m.Events()
		}
		m.End()
		err := m.Err()
		var le *Error
		if !errors.As(err, &le) || !errors.Is(le, c.want) || le.Offset != c.offset {
			t.Errorf("%q: got %v, want %v at %d", c.input, err, c.want, c.offset)
		}
	}
}

func TestProcessed(t *testing.T) {
	var m Machine
	s := strings.Repeat(`"a\b"'c'\ `, 1024*1024/10)
	for i := 0; i < len(s); i++ {
		m.Step(s[i], i)
		m.Events()
	}
	if m.Processed() != len(s) {
		t.Fatalf("processed = %d, want %d", m.Processed(), len(s))
	}
}
