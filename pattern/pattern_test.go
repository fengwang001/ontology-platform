package pattern

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		tokens []Token
	}{
		{name: "empty", input: "", tokens: nil},
		{name: "fold percent", input: "%%%a", tokens: []Token{{Kind: Any}, {Kind: Literal, Rune: 'a'}}},
		{name: "fold separated", input: "%%a%%", tokens: []Token{{Kind: Any}, {Kind: Literal, Rune: 'a'}, {Kind: Any}}},
		{name: "underscore rune", input: "caf_", tokens: []Token{{Kind: Literal, Rune: 'c'}, {Kind: Literal, Rune: 'a'}, {Kind: Literal, Rune: 'f'}, {Kind: One}}},
		{name: "emoji underscore", input: "_😀", tokens: []Token{{Kind: One}, {Kind: Literal, Rune: '😀'}}},
		{name: "escaped chars", input: `\%\_\\a`, tokens: []Token{{Kind: Literal, Rune: '%'}, {Kind: Literal, Rune: '_'}, {Kind: Literal, Rune: '\\'}, {Kind: Literal, Rune: 'a'}}},
		{name: "text symbols literal", input: `%\_\\`, tokens: []Token{{Kind: Any}, {Kind: Literal, Rune: '_'}, {Kind: Literal, Rune: '\\'}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			tokens := parsed.Tokens()
			if len(tokens) != len(tt.tokens) {
				t.Fatalf("len(tokens) = %d, want %d", len(tokens), len(tt.tokens))
			}
			for i := range tt.tokens {
				if tokens[i] != tt.tokens[i] {
					t.Fatalf("tokens[%d] = %+v, want %+v", i, tokens[i], tt.tokens[i])
				}
			}
		})
	}
}

func TestFoldShortensAndPreservesTokens(t *testing.T) {
	folded, err := Parse("%%%a")
	if err != nil {
		t.Fatal(err)
	}
	single, err := Parse("%a")
	if err != nil {
		t.Fatal(err)
	}

	if folded.NormalizedLength() != 2 || folded.NormalizedLength() >= len("%%%a") {
		t.Fatalf("folded length = %d", folded.NormalizedLength())
	}
	foldedTokens := folded.Tokens()
	singleTokens := single.Tokens()
	if len(foldedTokens) != len(singleTokens) {
		t.Fatalf("folded %d tokens, single %d", len(foldedTokens), len(singleTokens))
	}
	for i := range foldedTokens {
		if foldedTokens[i] != singleTokens[i] {
			t.Fatalf("tokens[%d] differs: %+v vs %+v", i, foldedTokens[i], singleTokens[i])
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		target error
		offset int
		side   Side
	}{
		{name: "invalid escape", input: `\a`, target: ErrInvalidEscape, offset: 0},
		{name: "trailing escape", input: `abc\`, target: ErrTrailingEscape, offset: 3},
		{name: "invalid utf8", input: "ab\xff", target: ErrInvalidUTF8, offset: 2, side: SidePattern},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if !errors.Is(err, tt.target) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.target)
			}
			switch typed := err.(type) {
			case InvalidUTF8Error:
				if typed.ByteOffset != tt.offset || typed.Side != tt.side {
					t.Fatalf("error = %+v", typed)
				}
			case InvalidEscapeError:
				if typed.ByteOffset != tt.offset {
					t.Fatalf("error = %+v", typed)
				}
			case TrailingEscapeError:
				if typed.ByteOffset != tt.offset {
					t.Fatalf("error = %+v", typed)
				}
			}
		})
	}
}

func TestTokensDoNotExposeMutableState(t *testing.T) {
	parsed, err := Parse("%%_a")
	if err != nil {
		t.Fatal(err)
	}
	tokens := parsed.Tokens()
	tokens[0] = Token{Kind: Literal, Rune: 'z'}
	if parsed.Tokens()[0] != (Token{Kind: Any}) {
		t.Fatal("pattern state changed through Tokens()")
	}
}
