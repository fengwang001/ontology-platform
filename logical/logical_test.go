package logical

import (
	"strings"
	"testing"
)

func texts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestReadBasic(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"plain", "a=b\n", []string{"a=b"}},
		{"blank", "\n\n", []string{"", ""}},
		{"comment keeps text", "  # hi\n", []string{"  # hi"}},
		{"crlf", "a=b\r\n", []string{"a=b"}},
		{"continuation", "k=v\\\n   w\n", []string{"k=vw"}},
		{"even slashes", "k=v\\\\\nx=y\n", []string{"k=v\\", "x=y"}},
		{"three slashes", "k=v\\\\\\\n  w\n", []string{"k=v\\w"}},
		{"comment in continuation", "k=a\\\n#b\n", []string{"k=a#b"}},
		{"eof lone slash", "k=v\\", []string{"k=v"}},
		{"eof two slashes", "k=v\\\\", []string{"k=v\\"}},
		{"blank line ends chain", "k=v\\\n\nx=y\n", []string{"k=v", "", "x=y"}},
		{"white line ends chain", "k=v\\\n   \n  w\n", []string{"k=v", "", "  w"}},
		{"white connector swallowed", "k=v\\\n   \\\n  w\n", []string{"k=vw"}},
		{"two empty connectors", "a\\\n\\\nb\n", []string{"ab"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Reader{}
			got := texts(r.Read([]byte(tc.in)))
			if !eqStrings(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestClassification(t *testing.T) {
	tests := []struct {
		in             string
		blank, comment bool
		firstPhys      int
	}{
		{"  \n", true, false, 1},
		{"# c\n", false, true, 1},
		{"!c\n", false, true, 1},
		{"k=v\n", false, false, 1},
		{"k=v\\\n w\n", false, false, 1},
	}
	for _, tc := range tests {
		r := Reader{}
		ls := r.Read([]byte(tc.in))
		l := ls[0]
		if l.Blank != tc.blank || l.Comment != tc.comment ||
			l.Segments[0].PhysicalLine != tc.firstPhys {
			t.Fatalf("%q: got blank=%v comment=%v phys=%d", tc.in,
				l.Blank, l.Comment, l.Segments[0].PhysicalLine)
		}
	}
}

func TestExaminedBound(t *testing.T) {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("k=")
		sb.WriteString(strings.Repeat("a", 900))
		sb.WriteString("\\\n")
	}
	sb.WriteString("end=1\n")
	data := []byte(sb.String())
	r := Reader{}
	r.Read(data)
	if got := r.Examined(); got > 2*int64(len(data)) {
		t.Fatalf("examined %d > 2*%d", got, len(data))
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
