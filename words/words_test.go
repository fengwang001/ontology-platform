package words

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "spaces only", input: " \t\n ", want: nil},
		{name: "plain", input: " a  b\tc\n", want: []string{"a", "b", "c"}},
		{name: "single literal backslash", input: `'a\b' '\a\'`, want: []string{`a\b`, `a\`}},
		{name: "double escapes", input: "\"a\\\"b\" \"a\\\\b\" \"a\\b\" \"a\\$\" \"a\\`b\"", want: []string{`a"b`, `a\b`, `a\b`, "a$", "a`b"}},
		{name: "double newline", input: "\"a\\\nb\"", want: []string{"ab"}},
		{name: "unquoted escape", input: `a\ b a\\ x\$y`, want: []string{"a b", `a\`, "x$y"}},
		{name: "unquoted newline", input: "a\\\nb", want: []string{"ab"}},
		{name: "join", input: `a"b"'c'`, want: []string{"abc"}},
		{name: "empty words", input: "a '' b \"\"'' a\"\"", want: []string{"a", "", "b", "", "a"}},
		{name: "line continuation alone", input: "\\\n", want: nil},
		{name: "utf8", input: "中 '文 字'\tΩ", want: []string{"中", "文 字", "Ω"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Split(test.input)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Split(%q) = %#v, %v; want %#v", test.input, got, err, test.want)
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		sentinel error
		offset   int
	}{
		{name: "single", input: `ab'cd`, sentinel: ErrUnterminatedSingle, offset: 2},
		{name: "double", input: "a\"bc", sentinel: ErrUnterminatedDouble, offset: 1},
		{name: "backslash", input: "ab\\", sentinel: ErrDanglingBackslash, offset: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Split(test.input)
			var syntax *SyntaxError
			if !errors.As(err, &syntax) || !errors.Is(err, test.sentinel) {
				t.Fatalf("Split error = %v, want %v", err, test.sentinel)
			}
			if syntax.Offset != test.offset {
				t.Fatalf("offset = %d, want %d", syntax.Offset, test.offset)
			}
		})
	}
}

func TestEveryChunkBoundary(t *testing.T) {
	tests := []string{
		`a\ b`, `a\` + "\n" + `b`, `'a\b'"c\$d"`, `""''`, `a"" b ''`,
		"x\\\"y `a b` $HOME # no expansion",
	}

	for _, input := range tests {
		want, err := Split(input)
		if err != nil {
			t.Fatalf("Split(%q): %v", input, err)
		}
		for cut := 0; cut <= len(input); cut++ {
			splitter := NewSplitter()
			if err := splitter.Feed(input[:cut]); err != nil {
				t.Fatalf("first Feed(%q,%d): %v", input, cut, err)
			}
			if err := splitter.Feed(input[cut:]); err != nil {
				t.Fatalf("second Feed(%q,%d): %v", input, cut, err)
			}
			if err := splitter.Close(); err != nil || !reflect.DeepEqual(splitter.Words(), want) {
				t.Fatalf("cut %d of %q = %#v, %v; want %#v", cut, input, splitter.Words(), err, want)
			}
		}

		stream := NewSplitter()
		for index := 0; index < len(input); index++ {
			if err := stream.Feed(input[index : index+1]); err != nil {
				t.Fatalf("byte Feed: %v", err)
			}
		}
		if err := stream.Close(); err != nil || !reflect.DeepEqual(stream.Words(), want) {
			t.Fatalf("byte chunks of %q = %#v, %v; want %#v", input, stream.Words(), err, want)
		}
	}
}

func TestQuoteRoundTrip(t *testing.T) {
	alphabet := []rune{'a', ' ', '\t', '\n', '\'', '"', '\\', '$', '`', '中'}
	random := rand.New(rand.NewSource(161))

	for iteration := 0; iteration < 500; iteration++ {
		count := random.Intn(8)
		original := make([]string, count)
		for wordIndex := range original {
			runes := make([]rune, random.Intn(9))
			for index := range runes {
				runes[index] = alphabet[random.Intn(len(alphabet))]
			}
			original[wordIndex] = string(runes)
		}

		got, err := Split(Quote(original))
		if err != nil || !reflect.DeepEqual(got, original) {
			t.Fatalf("round trip %#v: got %#v, %v", original, got, err)
		}
	}
}
