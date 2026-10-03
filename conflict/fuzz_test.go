package conflict

import (
	"math/rand"
	"strings"
	"testing"
)

// fuzzRng generates random documents and operation sequences.
type fuzzRng struct {
	r *rand.Rand
	L int
}

var fuzzMarkers = []byte{'<', '|', '=', '>'}

func (g *fuzzRng) line() string {
	switch g.r.Intn(10) {
	case 0, 1, 2, 3: // plain body
		words := []string{"", "a", "b", "c", "x y", "foo bar", "  pad", "tab\there"}
		return words[g.r.Intn(len(words))]
	case 4, 5: // marker run near L
		c := fuzzMarkers[g.r.Intn(4)]
		n := g.L + g.r.Intn(5) - 2 // L-2 .. L+2
		return strings.Repeat(string(c), n)
	case 6: // marker run with label (trailing spaces possible)
		c := fuzzMarkers[g.r.Intn(4)]
		n := g.L + g.r.Intn(3) - 1
		labels := []string{" lab", " x  ", " ", " a b ", ""}
		return strings.Repeat(string(c), n) + labels[g.r.Intn(len(labels))]
	case 7: // marker run followed by junk (body)
		c := fuzzMarkers[g.r.Intn(4)]
		return strings.Repeat(string(c), g.L) + ".junk"
	default: // plain word
		words := []string{"p", "q", "s", "line"}
		return words[g.r.Intn(len(words))]
	}
}

func (g *fuzzRng) text() string {
	n := g.r.Intn(25)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(g.line())
		sb.WriteByte('\n')
	}
	// Sometimes drop the trailing newline.
	if n > 0 && g.r.Intn(4) == 0 {
		s := sb.String()
		return s[:len(s)-1]
	}
	return sb.String()
}

func (g *fuzzRng) custom() []string {
	n := g.r.Intn(5)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if g.r.Intn(12) == 0 {
			out = append(out, "bad\nline")
		} else {
			out = append(out, g.line())
		}
	}
	return out
}

// TestNaiveDifferential runs 2000 random documents and operation
// sequences through both the real implementation and the naive
// reference, comparing every observable result. Each case logs its
// input, the produced output and the verdict rationale.
func TestNaiveDifferential(t *testing.T) {
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		r := rand.New(rand.NewSource(int64(tc)*7919 + 13))
		L := 7 + r.Intn(3)
		maxLines := []int{3, 8, 40, bigMax}[r.Intn(4)]
		g := &fuzzRng{r: r, L: L}
		text := g.text()

		d1, err1 := Parse(text, L, maxLines)
		nd, err2 := naiveParse(text, L, maxLines)
		if nErrString(err1) != nErrString(err2) {
			t.Fatalf("case %d: parse error mismatch: impl=%v naive=%v\ninput=%q L=%d max=%d",
				tc, err1, err2, text, L, maxLines)
		}
		if err1 != nil {
			t.Logf("case %d: input=%q L=%d maxLines=%d -> both rejected: %v (verdict: identical parse errors)",
				tc, text, L, maxLines, err1)
			continue
		}

		var ops []string
		nOps := 1 + r.Intn(15)
		for op := 0; op < nOps; op++ {
			var e1, e2 error
			var desc string
			switch r.Intn(3) {
			case 0, 1: // Resolve
				i := r.Intn(d1.Blocks()+2) - 1
				choice := Choice(r.Intn(6)) // includes invalid 5
				var cust []string
				if choice == Custom {
					cust = g.custom()
				}
				e1 = d1.Resolve(i, choice, cust)
				e2 = nd.nResolve(i, choice, cust)
				desc = "Resolve(" + itoa(i) + "," + itoa(int(choice)) + ")"
			default: // Normalize
				d1.Normalize()
				nd.nNormalize()
				desc = "Normalize()"
			}
			if nErrString(e1) != nErrString(e2) {
				t.Fatalf("case %d op %d %s: error mismatch: impl=%v naive=%v\ninput=%q",
					tc, op, desc, e1, e2, text)
			}
			ops = append(ops, desc+" -> "+nErrString(e1))
			if d1.Render() != nd.nRender() {
				t.Fatalf("case %d op %d %s: render mismatch:\nimpl=%q\nnaive=%q\ninput=%q",
					tc, op, desc, d1.Render(), nd.nRender(), text)
			}
			if d1.Blocks() != nd.nBlocks() {
				t.Fatalf("case %d op %d %s: blocks mismatch: impl=%d naive=%d",
					tc, op, desc, d1.Blocks(), nd.nBlocks())
			}
			if d1.MarkerLen() != nd.nMarkerLen() {
				t.Fatalf("case %d op %d %s: markerLen mismatch: impl=%d naive=%d",
					tc, op, desc, d1.MarkerLen(), nd.nMarkerLen())
			}
		}

		// Round-trip: reparse the rendered output with L'.
		out := d1.Render()
		d2, err := Parse(out, d1.MarkerLen(), bigMax)
		if err != nil {
			t.Fatalf("case %d: reparse of output failed: %v\noutput=%q", tc, err, out)
		}
		if d2.Render() != out {
			t.Fatalf("case %d: round trip unstable: %q vs %q", tc, out, d2.Render())
		}

		// Replay: the same operation sequence on a fresh document must
		// reproduce the identical text.
		d3, err := Parse(text, L, maxLines)
		if err != nil {
			t.Fatalf("case %d: replay parse failed: %v", tc, err)
		}
		r2 := rand.New(rand.NewSource(int64(tc)*7919 + 13))
		g2 := &fuzzRng{r: r2, L: L}
		_ = g2.text() // keep rng streams aligned
		for op := 0; op < nOps; op++ {
			switch r2.Intn(3) {
			case 0, 1:
				i := r2.Intn(d3.Blocks()+2) - 1
				choice := Choice(r2.Intn(6))
				var cust []string
				if choice == Custom {
					cust = g2.custom()
				}
				_ = d3.Resolve(i, choice, cust)
			default:
				d3.Normalize()
			}
		}
		if d3.Render() != out {
			t.Fatalf("case %d: replay diverged: %q vs %q", tc, d3.Render(), out)
		}

		t.Logf("case %d: input=%q L=%d maxLines=%d ops=%v output=%q "+
			"(verdict: impl==naive on render/blocks/markerLen/errors after every op; "+
			"round-trip stable under L'=%d; replay identical)",
			tc, text, L, maxLines, ops, out, d1.MarkerLen())
	}
}
