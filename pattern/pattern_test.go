package pattern

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseAndFold(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []Token
	}{
		{"empty", "", []Token{}},
		{"fold head", "%%%a", []Token{{AnySeq, 0}, {Literal, 'a'}}},
		{"fold middle", "a%%%b", []Token{{Literal, 'a'}, {AnySeq, 0}, {Literal, 'b'}}},
		{"fold tail", "a%%", []Token{{Literal, 'a'}, {AnySeq, 0}}},
		{"single each", "%_%", []Token{{AnySeq, 0}, {AnyChar, 0}, {AnySeq, 0}}},
		{"escaped percent literal", `\%`, []Token{{Literal, '%'}}},
		{"escaped underscore literal", `\_`, []Token{{Literal, '_'}}},
		{"escaped backslash literal", `\\`, []Token{{Literal, '\\'}}},
		{"escaped percent no fold", `\%%%`, []Token{{Literal, '%'}, {AnySeq, 0}}},
		{"cafe underscore", "caf_", []Token{
			{Literal, 'c'}, {Literal, 'a'}, {Literal, 'f'}, {AnyChar, 0},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.src, err)
			}
			got := p.Tokens()
			if len(got) != len(tc.want) {
				t.Fatalf("tokens = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("token %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
			if strings.Contains(tc.name, "fold") {
				raw := utf8.RuneCountInString(tc.src)
				if p.Len() >= raw {
					t.Fatalf("folded length %d not shorter than raw %d", p.Len(), raw)
				}
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind error
		pos  int
	}{
		{"bad escape char", `\a`, ErrPattern, 0},
		{"dangling backslash", `ab\`, ErrPattern, 2},
		{"invalid utf8 in pattern", "a\xffb", ErrPattern, 1},
		{"invalid utf8 after escape", "\\\xff", ErrPattern, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.kind) {
				t.Fatalf("error %v does not wrap %v", err, tc.kind)
			}
			var pe *PosError
			if !errors.As(err, &pe) || pe.BytePos() != tc.pos {
				t.Fatalf("error %v has wrong position, want %d", err, tc.pos)
			}
		})
	}
}

func TestTextErrorSide(t *testing.T) {
	err := NewTextError(3, "illegal UTF-8 encoding")
	if !errors.Is(err, ErrText) || errors.Is(err, ErrPattern) {
		t.Fatalf("text error side wrong: %v", err)
	}
	var pe *PosError
	if !errors.As(err, &pe) || pe.BytePos() != 3 {
		t.Fatalf("position wrong: %v", err)
	}
}
