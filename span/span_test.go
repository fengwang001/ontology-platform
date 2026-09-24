package span

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestMapQueries(t *testing.T) {
	cases := []struct {
		name   string
		build  func(*Mapper)
		orig   int
		out    int
		toOrig map[int]int
		toOut  map[int]int
	}{
		{name: "keep only", build: func(m *Mapper) { m.Keep(5) }, orig: 5, out: 5,
			toOrig: map[int]int{0: 0, 2: 2, 5: 5}, toOut: map[int]int{0: 0, 5: 5}},
		{name: "delete gap", build: func(m *Mapper) { m.Keep(1); m.Delete(2); m.Keep(1) },
			orig: 4, out: 2,
			toOrig: map[int]int{0: 0, 1: 3, 2: 4}, toOut: map[int]int{0: 0, 1: 1, 2: 1, 3: 1, 4: 2}},
		{name: "crlf", build: func(m *Mapper) { m.Keep(1); m.Delete(1); m.Remap(1); m.Keep(1) },
			orig: 4, out: 3,
			toOrig: map[int]int{0: 0, 1: 2, 2: 3, 3: 4}, toOut: map[int]int{0: 0, 1: 1, 2: 1, 3: 2, 4: 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Mapper{}
			c.build(m)
			if m.OrigLen() != c.orig || m.OutLen() != c.out {
				t.Fatalf("len = %d,%d want %d,%d", m.OrigLen(), m.OutLen(), c.orig, c.out)
			}
			for o, want := range c.toOrig {
				if got := m.ToOrig(o); got != want {
					t.Errorf("ToOrig(%d)=%d want %d", o, got, want)
				}
				if got := m.ToOut(want); got != o {
					t.Errorf("roundtrip ToOut(%d)=%d want %d", want, got, o)
				}
			}
			for i, want := range c.toOut {
				if got := m.ToOut(i); got != want {
					t.Errorf("ToOut(%d)=%d want %d", i, got, want)
				}
			}
		})
	}
}

func TestMonotonic(t *testing.T) {
	m := &Mapper{}
	m.Keep(3)
	m.Delete(4)
	m.Remap(1)
	m.Keep(2)
	m.Delete(2)
	for i := 1; i <= m.OrigLen(); i++ {
		if m.ToOut(i) < m.ToOut(i-1) {
			t.Fatalf("ToOut decreases at %d", i)
		}
	}
	for o := 1; o <= m.OutLen(); o++ {
		if m.ToOrig(o) < m.ToOrig(o-1) {
			t.Fatalf("ToOrig decreases at %d", o)
		}
	}
}

func TestBorrowTrimSynth(t *testing.T) {
	m := &Mapper{}
	m.Keep(2)
	m.Delete(3)
	if !m.Borrow() {
		t.Fatal("borrow failed")
	}
	if m.OrigLen() != 5 || m.OutLen() != 3 {
		t.Fatalf("after borrow %d,%d", m.OrigLen(), m.OutLen())
	}
	m.TrimOutput(1)
	if m.OrigLen() != 5 || m.OutLen() != 2 {
		t.Fatalf("after trim %d,%d", m.OrigLen(), m.OutLen())
	}
	if m.ToOut(5) != 2 {
		t.Fatal("endpoint after trim")
	}
	n := &Mapper{}
	n.Synth(0)
	if n.ToOrig(0) != 0 || n.OrigLen() != 0 || n.OutLen() != 1 {
		t.Fatal("synth mapping")
	}
}

func TestSegmentCountAndComplexity(t *testing.T) {
	plain := &Mapper{}
	plain.Keep(10 * 1024 * 1024)
	if plain.Segments() != 1 {
		t.Fatalf("plain segments=%d want 1", plain.Segments())
	}
	m := &Mapper{}
	var b strings.Builder
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&b, "line%d   \r\n", i)
	}
	data := b.String()
	if len(data) < 10_000_000 {
		for len(b.String()) < 10_000_000 {
			b.WriteString("x   \n")
		}
		data = b.String()
	}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		trim := strings.TrimRight(ln, " \t")
		m.Keep(len(trim))
		m.Delete(len(ln) - len(trim))
		m.Delete(1)
		m.Remap(1)
	}
	bound := 2*math.Log2(float64(m.Segments())) + 4
	for _, q := range []int{0, m.OutLen() / 3, m.OutLen() / 2, m.OutLen()} {
		m.ToOrig(q)
		if float64(m.Checks()) > bound {
			t.Fatalf("ToOrig checks %d > %.1f", m.Checks(), bound)
		}
	}
	for _, q := range []int{0, m.OrigLen() / 3, m.OrigLen()} {
		m.ToOut(q)
		if float64(m.Checks()) > bound {
			t.Fatalf("ToOut checks %d > %.1f", m.Checks(), bound)
		}
	}
}
