package token

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMasksAndValidates(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want Tokens
	}{
		{name: "single plain word", msg: "open", want: Tokens{"open"}},
		{name: "digit anywhere masks word", msg: "open file7 9 <*>", want: Tokens{"open", "<*>", "<*>", "<*>"}},
		{name: "non ascii byte counts as a byte", msg: "é 日", want: Tokens{"é", "日"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.msg)
			t.Logf("input=%q output=%v err=%v decision=%s", tt.msg, got, err, "validate words and replace words containing ASCII digit")
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("Parse = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRejectsInvalidShape(t *testing.T) {
	invalid := []string{
		"",
		" leading",
		"trailing ",
		"double  space",
		strings.Repeat("x", 65),
		strings.Repeat("w ", 32) + "w",
	}
	for _, msg := range invalid {
		t.Run(msg, func(t *testing.T) {
			got, err := Parse(msg)
			t.Logf("input=%q output=%v err=%v decision=%s", msg, got, err, "reject empty, leading or trailing space, long word, or too many words")
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}
