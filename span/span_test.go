package span_test

import (
	"math"
	"strings"
	"testing"

	"ontology/span"
)

func TestMapping(t *testing.T) {
	cases := []struct {
		name  string
		runs  []span.Run
		inLen int
		outLn int
	}{
		{
			name: "trailing_ws", // "ab  \n" -> "ab\n"
			runs: []span.Run{
				{Kind: span.Copy, IStart: 0, IEnd: 2, OStart: 0, OEnd: 2},
				{Kind: span.Delete, IStart: 2, IEnd: 4, OStart: 2, OEnd: 2},
				{Kind: span.Copy, IStart: 4, IEnd: 5, OStart: 2, OEnd: 3},
			},
			inLen: 5, outLn: 3,
		},
		{
			name: "crlf", // "a\r\nb" -> "a\nb"
			runs: []span.Run{
				{Kind: span.Copy, IStart: 0, IEnd: 1, OStart: 0, OEnd: 1},
				{Kind: span.Delete, IStart: 1, IEnd: 2, OStart: 1, OEnd: 1},
				{Kind: span.Copy, IStart: 2, IEnd: 4, OStart: 1, OEnd: 3},
			},
			inLen: 4, outLn: 3,
		},
		{
			name: "insert", // "abc" -> "abc\n"
			runs: []span.Run{
				{Kind: span.Copy, IStart: 0, IEnd: 3, OStart: 0, OEnd: 3},
				{Kind: span.Insert, IStart: 3, IEnd: 3, OStart: 3, OEnd: 4},
			},
			inLen: 3, outLn: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := span.New(tc.runs)
			if m.LenInput() != tc.inLen || m.LenOutput() != tc.outLn {
				t.Fatalf("lens = %d,%d want %d,%d", m.LenInput(), m.LenOutput(), tc.inLen, tc.outLn)
			}
			var prevO, prevI int
			for o := 0; o <= tc.outLn; o++ {
				i := m.ToOrig(o)
				if o > 0 && i < prevI {
					t.Fatalf("ToOrig not monotone at %d", o)
				}
				back := m.ToOut(i)
				if back != o {
					t.Fatalf("ToOut(ToOrig(%d)) = ToOut(%d) = %d", o, i, back)
				}
				prevI = i
			}
			for i := 0; i <= tc.inLen; i++ {
				o := m.ToOut(i)
				if i > 0 && o < prevO {
					t.Fatalf("ToOut not monotone at %d", i)
				}
				prevO = o
			}
		})
	}
}

func TestDeletedOffsets(t *testing.T) {
	m := span.New([]span.Run{
		{Kind: span.Copy, IStart: 0, IEnd: 2, OStart: 0, OEnd: 2},
		{Kind: span.Delete, IStart: 2, IEnd: 4, OStart: 2, OEnd: 2},
		{Kind: span.Copy, IStart: 4, IEnd: 5, OStart: 2, OEnd: 3},
	})
	for i, want := range map[int]int{2: 2, 3: 2, 4: 2} {
		if got := m.ToOut(i); got != want {
			t.Fatalf("ToOut(%d)=%d want %d", i, got, want)
		}
	}
	if m.ToOrig(2) != 4 {
		t.Fatalf("ToOrig(newline boundary) = %d want 4", m.ToOrig(2))
	}
}

func buildHuge(lines int) *span.Map {
	var runs []span.Run
	ic, oc := 0, 0
	for n := 0; n < lines; n++ {
		runs = append(runs,
			span.Run{Kind: span.Copy, IStart: ic, IEnd: ic + 3, OStart: oc, OEnd: oc + 3},
			span.Run{Kind: span.Delete, IStart: ic + 3, IEnd: ic + 5, OStart: oc + 3, OEnd: oc + 3},
			span.Run{Kind: span.Copy, IStart: ic + 5, IEnd: ic + 7, OStart: oc + 3, OEnd: oc + 4},
		)
		ic += 7
		oc += 4
	}
	return span.New(runs)
}

func TestQueryComplexity(t *testing.T) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"100k_lines", 100000},
		{"10MB", 1500000},
	}
	for _, sz := range sizes {
		t.Run(sz.name, func(t *testing.T) {
			m := buildHuge(sz.lines)
			bound := 2*math.Ceil(math.Log2(float64(len(m.Runs())))) + 4
			for _, o := range []int{0, m.LenOutput() / 3, m.LenOutput() - 1, m.LenOutput()} {
				_ = m.ToOrig(o)
				if m.LastChecked() > int(bound) {
					t.Fatalf("checked %d > bound %v", m.LastChecked(), bound)
				}
			}
			for _, i := range []int{0, m.LenInput() / 7, m.LenInput()} {
				_ = m.ToOut(i)
				if m.LastChecked() > int(bound) {
					t.Fatalf("checked %d > bound %v", m.LastChecked(), bound)
				}
			}
		})
	}
}

func TestRunCountBounded(t *testing.T) {
	text := strings.Repeat("\n", 10*1024*1024)
	m := span.New([]span.Run{{Kind: span.Copy, IStart: 0, IEnd: len(text), OStart: 0, OEnd: len(text)}})
	if len(m.Runs()) > 1 {
		t.Fatalf("runs = %d, want <= 1 for deletion-free text", len(m.Runs()))
	}
}
