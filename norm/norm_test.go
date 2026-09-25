package norm_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/norm"
)

var policies = []norm.Policy{norm.Keep, norm.EnsureOne, norm.Strip}

func run(t *testing.T, s string, o norm.Options) (string, *norm.Norm) {
	n := norm.New(o)
	if _, err := n.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	return string(n.Output()), n
}

func refNorm(s string, p norm.Policy) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	s = strings.Join(lines, "\n")
	if p != norm.Keep {
		s = strings.TrimRight(s, "\n")
		if p == norm.EnsureOne || s != "" {
			s += "\n"
		}
	}
	return s
}

func TestSemantics(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\r\n", "\n"}, {"\r", "\n"}, {"\n", "\n"}, {"\r\r\n", "\n\n"},
		{"a\rb\rc", "a\nb\nc"}, {"a  \n", "a\n"}, {"  \n", "\n"},
		{"a \t b\n", "a \t b\n"}, {"a  ", "a"}, {"a\t\nb \r\n", "a\nb\n"},
		{"a\x00b\n", "a\x00b\n"}, {"\xff\xfe \n", "\xff\xfe\n"},
	}
	for _, c := range cases {
		if got, _ := run(t, c.in, norm.Options{}); got != c.want {
			t.Errorf("N(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
func TestPolicyTable(t *testing.T) {
	in := []string{"", "\n", "\n\n", "  \r\n"}
	want := map[norm.Policy][]string{norm.Keep: {"", "\n", "\n\n", "\n"}, norm.EnsureOne: {"\n", "\n", "\n", "\n"}, norm.Strip: {"", "", "", ""}}
	for _, p := range policies {
		for i, s := range in {
			if got, _ := run(t, s, norm.Options{Policy: p}); got != want[p][i] {
				t.Errorf("policy %d N(%q)=%q want %q", p, s, got, want[p][i])
			}
		}
	}
}

const mixed = "a  \r\nb\t\rc \n  \r\n\ndone  "

func TestSplitsAndTruncation(t *testing.T) {
	for _, p := range policies {
		o := norm.Options{Policy: p}
		whole, _ := run(t, mixed, o)
		for c := 0; c <= len(mixed); c++ {
			n, m := norm.New(o), norm.New(o)
			n.Write([]byte(mixed[:c]))
			n.Write([]byte(mixed[c:]))
			n.Close()
			m.Write([]byte(mixed[:c]))
			m.Close()
			if string(n.Output()) != whole || string(m.Output()) != refNorm(mixed[:c], p) {
				t.Fatalf("policy %d cut %d: %q / %q", p, c, n.Output(), m.Output())
			}
		}
		if whole != refNorm(mixed, p) {
			t.Fatalf("policy %d: %q != ref %q", p, whole, refNorm(mixed, p))
		}
	}
}
func TestIdempotent(t *testing.T) {
	in := []string{"", "\n", "a", "a  \r\nb\t\r", "  \r\n", "x\n\n\n", "a  "}
	for _, p := range policies {
		for _, s := range in {
			once, _ := run(t, s, norm.Options{Policy: p})
			twice, _ := run(t, once, norm.Options{Policy: p})
			if once != twice {
				t.Errorf("policy %d: N(N(%q))=%q != N(x)=%q", p, s, twice, once)
			}
		}
	}
}
func TestMapping(t *testing.T) {
	_, n := run(t, mixed, norm.Options{})
	m := n.Map()
	for o := 0; o <= m.OutLen(); o++ { // Keep policy: no synthesized bytes
		if m.ToOut(m.ToOrig(o)) != o {
			t.Fatalf("ToOut(ToOrig(%d))=%d", o, m.ToOut(m.ToOrig(o)))
		}
	}
	for i := 0; i < m.OrigLen(); i++ {
		if m.ToOut(i) > m.ToOut(i+1) || m.ToOrig(i) > m.ToOrig(i+1) {
			t.Fatalf("not monotonic at %d", i)
		}
	}
	delIn := []string{"a  \n", "a  \n", "\r\n", "ab\r\nc"}
	delOff, delWant := []int{1, 2, 0, 2}, []int{1, 1, 0, 2}
	for i, s := range delIn {
		_, nn := run(t, s, norm.Options{})
		if got := nn.Map().ToOut(delOff[i]); got != delWant[i] {
			t.Errorf("ToOut(%q,%d)=%d want %d", s, delOff[i], got, delWant[i])
		}
	}
}
func TestErrors(t *testing.T) {
	opts := []norm.Options{{Strict: true}, {MaxWS: 2}, {MaxOut: 2}}
	ins := []string{"a\x00b", "a   b", "abc"}
	kinds := []norm.Kind{norm.KindNUL, norm.KindWS, norm.KindOut}
	offs := []int{1, 3, 2}
	var e *norm.Error
	for i := range opts {
		n := norm.New(opts[i])
		if _, err := n.Write([]byte(ins[i])); !errors.As(err, &e) || e.Kind != kinds[i] || e.Off != offs[i] {
			t.Errorf("case %d: %v", i, err)
		}
		if _, err := n.Write([]byte("x")); err == nil {
			t.Errorf("case %d: write after error succeeded", i)
		}
	}
	n := norm.New(norm.Options{Strict: true})
	n.Write([]byte("a\x00b"))
	if string(n.Output()) != "a" {
		t.Fatal("kept output")
	}
	n4 := norm.New(norm.Options{})
	n4.Close()
	if _, err := n4.Write([]byte("x")); !errors.As(err, &e) || e.Kind != norm.KindClosed {
		t.Fatal("write after close")
	}
}
