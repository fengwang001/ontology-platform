package lex_test

import (
	"strings"
	"testing"

	"ontology/lex"
)

// TestProcessedCounter proves the machine touches every input byte exactly
// once, even when fed one byte at a time: 1 MB in, 1 MB counted.
func TestProcessedCounter(t *testing.T) {
	chunk := `a"b\\c\"d" 'e\f' g\h` + "\\\n" + "i $x`y` "
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString(chunk)
	}
	in := []byte(sb.String())
	var words int
	l := lex.New(func(string) { words++ })
	for i := 0; i < len(in); i++ {
		l.Feed(in[i : i+1])
	}
	if err := l.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	if l.Processed() != len(in) {
		t.Fatalf("Processed() = %d, want %d", l.Processed(), len(in))
	}
	if words == 0 {
		t.Fatal("no words emitted")
	}
}
