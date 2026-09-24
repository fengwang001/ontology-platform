package logical

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func scan(t *testing.T, input string) []Line {
	t.Helper()
	s := NewScanner(strings.NewReader(input))
	var got []Line
	for {
		ln, err := s.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ln)
	}
}

func TestNext(t *testing.T) {
	tests := []struct {
		name  string
	input string
	want  []Line
	}{
		{"basic", "a=b\n", []Line{{Text: "a=b", Line: 1}}},
		{"blank and comment", "\n  \n# c\n!\nx=y\n", []Line{
			{Text: "# c", Line: 3, Comment: true},
			{Text: "x=y", Line: 5},
		}},
		{"continuation strips leading ws", "k=v\\\n   w\n", []Line{
			{Text: "k=v\\\nw", Line: 1},
		}},
		{"even backslashes no continuation", "k=v\\\\\nx=y\n", []Line{
			{Text: "k=v\\\\", Line: 1}, {Text: "x=y", Line: 2},
		}},
		{"triple backslash continuation", "k=v\\\\\\\n  w\n", []Line{
			{Text: "k=v\\\\\\\nw", Line: 1},
		}},
		{"hash on continued line is data", "k=a\\\n#b\n", []Line{
			{Text: "k=a\\\n#b", Line: 1},
		}},
		{"trailing backslash at EOF dropped later", "k=v\\", []Line{
			{Text: "k=v\\", Line: 1},
		}},
		{"blank continued line ends logical line", "a\\\n\n", []Line{
			{Text: "a\\", Line: 1},
		}},
		{"whitespace-only continued line ends it", "a\\\n   \nb=2\n", []Line{
			{Text: "a\\", Line: 1}, {Text: "b=2", Line: 3},
		}},
		{"crlf", "a=b\r\nc=d\r\n", []Line{
			{Text: "a=b", Line: 1}, {Text: "c=d", Line: 2},
		}},
		{"lone cr", "a=b\rc=d", []Line{
			{Text: "a=b", Line: 1}, {Text: "c=d", Line: 2},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scan(t, tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestChecksBound(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("part")
		if i < 999 {
			b.WriteString("\\\n        ")
		}
	}
	b.WriteString("\n")
	input := b.String()
	for len(input) < 1<<20 {
		input += input
	}

	s := NewScanner(strings.NewReader(input))
	for {
		if _, err := s.Next(); err == io.EOF {
			break
		}
	}
	if got := s.Checks(); got > 2*int64(len(input)) {
		t.Fatalf("checks %d exceed 2*len %d", got, 2*int64(len(input)))
	}
}
