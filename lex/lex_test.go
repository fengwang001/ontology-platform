package lex

import (
	"strings"
	"testing"
)

// TestStepCountIsLinear proves every input byte is processed exactly
// once: 1 MB fed byte by byte must bump the counter to len(input).
func TestStepCountIsLinear(t *testing.T) {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("a\"b\\$c\" 'd\\e' f\\ g\\\nh \"i\\j\" \\\\\n")
	}
	in := sb.String()[:1<<20]
	var lx Lexer
	var ev []Event
	for i := 0; i < len(in); i++ {
		ev = lx.Step(in[i], ev[:0])
	}
	if lx.n != len(in) {
		t.Fatalf("processed %d bytes, want %d", lx.n, len(in))
	}
	if lx.Count() != len(in) {
		t.Fatalf("Count() = %d, want %d", lx.Count(), len(in))
	}
}

// TestStepEvents pins the event stream of representative inputs.
func TestStepEvents(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Event
	}{
		{"continuation", "\\\n", nil},
		{"empty word", `''`, []Event{{Op: OpBegin}, {Op: OpEnd}}},
		{"escape split", "a\\b", []Event{
			{Op: OpBegin}, {Op: OpEmit, Byte: 'a'},
			{Op: OpEmit, Byte: 'b'}, {Op: OpEnd},
		}},
		{"dq kept backslash", `"a\b"`, []Event{
			{Op: OpBegin}, {Op: OpEmit, Byte: 'a'},
			{Op: OpEmit, Byte: '\\'}, {Op: OpEmit, Byte: 'b'}, {Op: OpEnd},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lx Lexer
			var ev []Event
			for i := 0; i < len(tc.in); i++ {
				ev = lx.Step(tc.in[i], ev)
			}
			var err error
			if ev, err = lx.Close(ev); err != nil {
				t.Fatal(err)
			}
			if len(ev) != len(tc.want) {
				t.Fatalf("got %v, want %v", ev, tc.want)
			}
			for i := range ev {
				if ev[i] != tc.want[i] {
					t.Fatalf("event %d = %v, want %v", i, ev[i], tc.want[i])
				}
			}
		})
	}
}
