package span_test

import (
	"math"
	"strings"
	"testing"

	"ontology/norm"
)

func same(t *testing.T, got, want []byte, msg string) {
	t.Helper()
	if string(got) != string(want) {
		t.Errorf("%s: got %q want %q", msg, got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"crlf-drops-cr", "a  \r\nb\t\nc\r\nd"},
		{"lone-cr", "x \ry"},
		{"blank-ws-lines", "  \n\t\r\nz\t \r\n"},
		{"no-change", "ab cd\nef"},
		{"trailing-ws-no-nl", "hello   "},
	}
	for _, c := range cases {
		for _, e := range []norm.Ending{norm.Preserve, norm.Ensure, norm.Trim} {
			out, m, err := norm.Run([]byte(c.in), norm.Config{Ending: e})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			for o := 0; o <= len(out); o++ {
				if m.ToOut(m.ToOrig(o)) != o {
					t.Fatalf("%s: roundtrip fails at o=%d", c.name, o)
				}
			}
			prev := -1
			for i := 0; i <= len(c.in); i++ {
				got := m.ToOut(i)
				if got < prev {
					t.Fatalf("%s: ToOut decreases at %d", c.name, i)
				}
				prev = got
			}
			po := -1
			for o := 0; o <= len(out); o++ {
				if m.ToOrig(o) < po {
					t.Fatalf("%s: ToOrig decreases at %d", c.name, o)
				}
				po = m.ToOrig(o)
			}
			if i := strings.IndexByte(c.in, '\r'); i >= 0 {
				if m.ToOut(i) >= len(out) || out[m.ToOut(i)] != '\n' {
					t.Fatalf("%s: deleted CR cursor not before LF", c.name)
				}
			}
		}
	}
}

func TestScale(t *testing.T) {
	mix := func(n, width int) []byte {
		var b strings.Builder
		ends := []string{"\r\n", "\r", "\n"}
		for i := 0; i < n; i++ {
			b.WriteString("line")
			b.WriteString(strings.Repeat(" ", width))
			b.WriteString(ends[i%3])
		}
		return []byte(b.String())
	}
	cases := []struct {
		name string
		in   []byte
	}{
		{"100k-lines", mix(100000, 0)},
		{"10MB", mix(100000, 96)},
		{"10MB-pure-nl", []byte(strings.Repeat("\n", 10_000_000))},
	}
	for _, c := range cases {
		out, m, err := norm.Run(c.in, norm.Config{Ending: norm.Preserve})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		bound := int(2*math.Log2(float64(m.Segments()))) + 4
		for step, q := 0, 0; q <= len(out); q += len(out)/137 + 1 {
			m.ToOrig(q)
			if n := m.LastChecked(); n > bound {
				t.Fatalf("%s: ToOrig checked %d > %d", c.name, n, bound)
			}
			m.ToOut(q % len(c.in))
			if n := m.LastChecked(); n > bound {
				t.Fatalf("%s: ToOut checked %d > %d", c.name, n, bound)
			}
			step++
		}
		if c.name == "10MB-pure-nl" && m.Segments() > 3 {
			t.Fatalf("%s: segments grow with output: %d", c.name, m.Segments())
		}
		if len(out) == 0 {
			t.Fatalf("%s: empty output", c.name)
		}
	}
}
