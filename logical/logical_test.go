package logical

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Line
	}{
		{"comment", "# x\n! y\nk=v\n", []Line{{"k=v", 3, 1}}},
		{"blank-and-indent", "  \n  k=v\n", []Line{{"k=v", 2, 3}}},
		{"cont-odd", "k=v\\\n   w\n", []Line{{"k=vw", 1, 1}}},
		{"cont-even", "k=v\\\\\nx=y\n", []Line{{"k=v\\\\", 1, 1}, {"x=y", 2, 1}}},
		{"cont-triple", "k=v\\\\\\\n  w\n", []Line{{"k=v\\\\w", 1, 1}}},
		{"cont-hash", "k=a\\\n#b\n", []Line{{"k=a#b", 1, 1}}},
		{"cont-eof", "k=v\\", []Line{{"k=v", 1, 1}}},
		{"cont-blank-line", "k=v\\\n  \nw\n", []Line{{"k=v", 1, 1}, {"w", 3, 1}}},
		{"cont-ws-backslash", "k=v\\\n  \\\n  w\n", []Line{{"k=vw", 1, 1}}},
		{"crlf", "a=1\r\nb=2\r\n", []Line{{"a=1", 1, 1}, {"b=2", 2, 1}}},
		{"empty", "", nil},
		{"only-comments", "# a\n! b\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Split(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestCheckedLinear(t *testing.T) {
	var b strings.Builder
	for b.Len() < 1<<20 {
		b.WriteString("k=")
		for i := 0; i < 100; i++ {
			b.WriteString(strings.Repeat("v", 99) + "\\\n")
		}
		b.WriteString("end\n")
	}
	Reset()
	lines, err := Split(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("no logical lines")
	}
	if got, limit := Checked(), 2*int64(b.Len()); got > limit {
		t.Errorf("checked %d bytes, input %d, limit %d", got, b.Len(), limit)
	}
}
