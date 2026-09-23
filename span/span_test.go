package span

import (
	"math"
	"testing"

	"ontology/eol"
	"ontology/ws"
)

func TestEOL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want eol.Kind
	}{
		{"empty", "", eol.None}, {"lf", "\n", eol.LF}, {"crlf", "\r\n", eol.CRLF},
		{"cr_lone", "\r\ra", eol.CR}, {"cr_a", "\ra", eol.CR}, {"maybe", "\r", eol.MaybeCR},
		{"other", "a", eol.None}, {"resolve_cr", "", eol.None},
	}
	for _, c := range cases {
		if got := eol.At([]byte(c.in)); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if eol.Resolve(eol.MaybeCR, '\n', true) != eol.CRLF {
		t.Error("resolve CRLF")
	}
	if eol.Resolve(eol.MaybeCR, 'a', true) != eol.CR {
		t.Error("resolve CR")
	}
	if eol.Resolve(eol.MaybeCR, 0, false) != eol.CR {
		t.Error("resolve EOF CR")
	}
}

func TestWS(t *testing.T) {
	cases := []struct {
		b    byte
		want bool
	}{{' ', true}, {'\t', true}, {'a', false}, {'\n', false}, {'\r', false}, {0, false}}
	for _, c := range cases {
		if ws.Is(c.b) != c.want {
			t.Errorf("Is(%q)", c.b)
		}
	}
	p := ws.New(0)
	if !p.Empty() || p.Start() != -1 {
		t.Fatal("new pending not empty")
	}
	p.Add(' ', 3)
	p.Add('\t', 4)
	if p.Len() != 2 || p.Start() != 3 {
		t.Fatal("pending bookkeeping")
	}
	if string(p.Take()) != " \t" || !p.Empty() {
		t.Fatal("take")
	}
}

func TestSpanMapping(t *testing.T) {
	// original: "ab  \r\ncd" (indices 0..6); output: "ab\ncd"
	// retain "ab"(0,2), retain "\n" orig5 out2, retain "cd"(6,2)
	cases := []struct {
		b                      *Builder
		origLen, outLen        int
		toOrig, toOut          []int
	}{
		{buildABNLCD(), 8, 5,
			[]int{0, 1, 5, 6, 7, 8}, // output offsets 0..5 -> orig
			[]int{0, 1, 2, 2, 2, 2, 3, 4, 5}}, // orig 0..8 -> out
	}
	for _, c := range cases {
		c.b.OrigEnd(c.origLen)
		tb := c.b.Build()
		if tb.OutLen() != c.outLen {
			t.Fatalf("outlen %d", tb.OutLen())
		}
		for o, want := range c.toOrig {
			if got := tb.ToOrig(o); got != want {
				t.Errorf("ToOrig(%d)=%d want %d", o, got, want)
			}
			if got := tb.ToOut(tb.ToOrig(o)); got != o {
				t.Errorf("inverse at %d: %d", o, got)
			}
		}
		for i, want := range c.toOut {
			if got := tb.ToOut(i); got != want {
				t.Errorf("ToOut(%d)=%d want %d", i, got, want)
			}
		}
		assertMono(t, tb)
	}
}

func buildABNLCD() *Builder {
	b := &Builder{}
	b.Retain(0, 2)
	b.Retain(5, 1)
	b.Retain(6, 2)
	return b
}

func assertMono(t *testing.T, tb *Table) {
	t.Helper()
	prev := -1
	for i := 0; i <= tb.OrigLen(); i++ {
		if got := tb.ToOut(i); got < prev {
			t.Fatalf("ToOut not mono at %d", i)
		} else {
			prev = got
		}
	}
	prev = -1
	for o := 0; o <= tb.OutLen(); o++ {
		if got := tb.ToOrig(o); got < prev {
			t.Fatalf("ToOrig not mono at %d", o)
		} else {
			prev = got
		}
	}
}

func TestMergeAndBinaryBound(t *testing.T) {
	b := &Builder{}
	for i := 0; i < 1000; i++ {
		b.Retain(i*3, 1) // gaps between, so no merge
	}
	tb := b.Build()
	if len(tb.entries) != 1000 {
		t.Fatalf("entries %d", len(tb.entries))
	}
	tb.ToOrig(1)
	tb.ToOut(1)
	bound := 2*math.Log2(1000) + 4
	if float64(tb.LastCheck()) > bound {
		t.Fatalf("checks %d > bound %v", tb.LastCheck(), bound)
	}
	// Adjacent retains merge: 10MB pure '\n' must stay one entry.
	m := &Builder{}
	m.Retain(0, 10_000_000)
	mt := m.Build()
	if len(mt.entries) != 1 {
		t.Fatalf("pure text entries %d", len(mt.entries))
	}
}
