package span_test

import (
	"bytes"
	"math/bits"
	"strings"
	"testing"

	"ontology/norm"
	"ontology/span"
)

func checkMap(t *testing.T, in, out []byte, m *span.Map, strictInverse bool) {
	t.Helper()
	prev := -1
	for o := 0; o <= m.OutLen(); o++ {
		i := m.ToOrig(o)
		if i < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		}
		prev = i
		if i < 0 || i > m.OrigLen() {
			t.Fatalf("ToOrig(%d)=%d out of range", o, i)
		}
		if strictInverse && m.ToOut(i) != o {
			t.Fatalf("inverse fails at out %d -> orig %d -> out %d", o, i, m.ToOut(i))
		}
	}
	prev = -1
	for i := 0; i <= m.OrigLen(); i++ {
		o := m.ToOut(i)
		if o < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		}
		prev = o
		if o < 0 || o > m.OutLen() {
			t.Fatalf("ToOut(%d)=%d out of range", i, o)
		}
	}
	for o := 0; o < m.OutLen(); o++ {
		i := m.ToOrig(o)
		if i < len(in) && in[i] != out[o] && !(in[i] == '\r' && out[o] == '\n') {
			t.Fatalf("content mismatch at out %d (orig %d): %q vs %q", o, i, out[o], in[i])
		}
	}
}

func TestMapInverse(t *testing.T) {
	cases := []string{
		"", "abc", "ab  \r\nc\t\nd", " \r\n", "\r\r\n  x \r\n",
		"no newline   ", "\n\n\n", "a\rb", "x\r\n y\tz \n",
	}
	for _, p := range []norm.Policy{norm.Keep, norm.EnsureOne, norm.TrimBlank} {
		for _, in := range cases {
			out, m, err := norm.Normalize([]byte(in), norm.Config{Policy: p})
			if err != nil {
				t.Fatal(err)
			}
			strict := len(out) <= len(in)
			checkMap(t, []byte(in), out, m, strict)
			if !strict {
				for o := 0; o < m.OutLen(); o++ {
					if m.ToOut(m.ToOrig(o)) != o {
						t.Fatalf("non-strict inverse fails at %d (in=%q out=%q orig=%d)", o, in, out, m.ToOrig(o))
					}
				}
			}
		}
	}
}

func TestDeletedByteMapping(t *testing.T) {
	in := []byte("ab   \r\nc")
	_, m, err := norm.Normalize(in, norm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	nl := m.ToOut(6)
	if nl != 2 {
		t.Fatalf("newline out offset = %d, want 2", nl)
	}
	for _, i := range []int{2, 3, 4, 5} {
		if got := m.ToOut(i); got != 2 {
			t.Fatalf("deleted ws ToOut(%d)=%d, want newline 2", i, got)
		}
	}
	for o := 0; o <= m.OutLen(); o++ {
		if m.ToOut(m.ToOrig(o)) != o {
			t.Fatalf("inverse at %d", o)
		}
	}
}

func budget(n int) int { return 2*(bits.Len(uint(n))) + 4 }

func TestScaleProbes(t *testing.T) {
	scales := []struct {
		name string
		data []byte
	}{
		{"100k-lines", bytes.Repeat([]byte("row  \t\r\n"), 100000)},
		{"10MB", build10MB()},
	}
	for _, sc := range scales {
		out, m, err := norm.Normalize(sc.data, norm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		bound := budget(m.NumRuns())
		var worst int
		for _, o := range []int{0, m.OutLen() / 2, m.OutLen() - 1, m.OutLen()} {
			if o < 0 || o > m.OutLen() {
				continue
			}
			_ = m.ToOrig(o)
			if m.Probes() > worst {
				worst = m.Probes()
			}
			_ = m.ToOut(m.OrigLen() / 2)
			if m.Probes() > worst {
				worst = m.Probes()
			}
		}
		if worst > bound {
			t.Fatalf("%s: probes %d > budget %d (runs=%d)", sc.name, worst, bound, m.NumRuns())
		}
		if sc.name == "10MB" && m.NumRuns() > 2 {
			t.Fatalf("%s: runs %d, want constant", sc.name, m.NumRuns())
		}
		t.Logf("%s: bytes=%d out=%d runs=%d worstProbes=%d budget=%d",
			sc.name, len(sc.data), len(out), m.NumRuns(), worst, bound)
	}
}

func build10MB() []byte {
	line := strings.Repeat("a", 79) + "\n"
	return bytes.Repeat([]byte(line), 125000)
}
