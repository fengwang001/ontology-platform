package lex

import (
	"errors"
	"strings"
	"testing"
)

func TestProcessedCountsEveryByteOnce(t *testing.T) {
	input := strings.Repeat(`'a\b'"c\$d" e\ f\`+"\n", 58255)
	machine := &Machine{}

	for index := 0; index < len(input); index++ {
		machine.Step(input[index], index)
	}

	if got := machine.Processed(); got != len(input) {
		t.Fatalf("processed = %d, want %d", got, len(input))
	}
	if len(input) < 1024*1024 {
		t.Fatalf("test input length = %d, want at least 1 MiB", len(input))
	}
}

func TestSyntaxErrors(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   error
		offset int
	}{
		{name: "single", input: `ab'cd`, want: ErrUnterminatedSingle, offset: 2},
		{name: "double", input: "a\"bc", want: ErrUnterminatedDouble, offset: 1},
		{name: "double escaped", input: `"a\$`, want: ErrUnterminatedDouble, offset: 0},
		{name: "backslash", input: "ab\\", want: ErrDanglingBackslash, offset: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			machine := &Machine{}
			for index := 0; index < len(test.input); index++ {
				machine.Step(test.input[index], index)
			}
			err := machine.Close()
			var syntax *SyntaxError
			if !errors.As(err, &syntax) || !errors.Is(err, test.want) {
				t.Fatalf("Close error = %v, want %v", err, test.want)
			}
			if syntax.Offset != test.offset {
				t.Fatalf("offset = %d, want %d", syntax.Offset, test.offset)
			}
		})
	}
}

func TestOneByteChunksMatchState(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		pauseAt int
		want    State
		inWord  bool
	}{
		{name: "after backslash", input: `a\ b`, pauseAt: 2, want: Escaped, inWord: true},
		{name: "in single quote", input: `'ab`, pauseAt: 2, want: Single, inWord: true},
		{name: "in double escape", input: `"a\$b"`, pauseAt: 3, want: DoubleEscaped, inWord: true},
		{name: "empty quote remains word", input: `''`, pauseAt: 2, want: Normal, inWord: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			machine := &Machine{}
			for index := 0; index < test.pauseAt; index++ {
				machine.Step(test.input[index], index)
			}
			if machine.State() != test.want || machine.InWord() != test.inWord {
				t.Fatalf("got state=%v inWord=%v, want state=%v inWord=%v", machine.State(), machine.InWord(), test.want, test.inWord)
			}
		})
	}
}
