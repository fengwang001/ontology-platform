package logical_test

import (
	"slices"
	"strings"
	"testing"

	"ontology/logical"
)

func TestLinesTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"comment and blank", "# c\n\n! d\nk=v", []string{"k=v"}},
		{"continuation strips ws", "k=v\\\n   w", []string{"k=vw"}},
		{"even slashes no cont", "k=v\\\\\nx=y", []string{`k=v\\`, "x=y"}},
		{"hash in cont not comment", "k=a\\\n#b", []string{"k=a#b"}},
		{"blank cont ends line", "k=v\\\n  \nx=y", []string{"k=v", "x=y"}},
		{"trailing slash at eof", "k=v\\", []string{"k=v"}},
		{"lone slash then blank", "\\\n\nk=v", []string{"k=v"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, err := logical.Lines(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(lines))
			for i, l := range lines {
				got[i] = l.Text
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("Lines(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCheckedLinear(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("k=")
	for sb.Len() < 1<<20 {
		sb.WriteString(strings.Repeat("x", 500) + "\\\n")
	}
	sb.WriteString("end\n")
	in := sb.String()
	lines, err := logical.Lines(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d logical lines, want 1", len(lines))
	}
	if got, limit := logical.Checked(), int64(2*len(in)); got > limit {
		t.Fatalf("checked %d bytes, limit %d", got, limit)
	}
}
