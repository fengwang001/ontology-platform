package align

import (
	"testing"

	"ontology/dist"
)

func TestScriptTable(t *testing.T) {
	cases := []struct {
		name   string
		a, b   string
		expect int
	}{
		{"transpose", "CA", "AC", 1},
		{"transpose insert", "CA", "ABC", 2},
		{"lower transpose", "ab", "ba", 1},
		{"transpose delete", "abc", "ca", 2},
		{"kitten", "kitten", "sitting", 3},
		{"illegal byte", "\xff", "\xfe", 1},
		{"cjk", "日本", "本日", 1},
		{"empty src", "", "abc", 3},
		{"empty dst", "abc", "", 3},
		{"identical", "abc", "abc", 0},
		{"far", "abc", "bca", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, err := Script(c.a, c.b)
			if err != nil {
				t.Fatal(err)
			}
			if got := Cost(sc); got != c.expect {
				t.Fatalf("cost=%d want %d; script=%v", got, c.expect, sc)
			}
			d, _ := dist.Distance(c.a, c.b, 1<<62)
			if Cost(sc) != d {
				t.Fatalf("script cost %d != distance %d", Cost(sc), d)
			}
			got := Apply(c.a, sc)
			if !runesEqual(dist.DecodeRunes(got), dist.DecodeRunes(c.b)) {
				t.Fatalf("Apply(%q)=%q want %q", c.a, got, c.b)
			}
			if err := validAdjacency(c.a, sc); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func runesEqual(x, y []rune) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func TestExhaustiveScripts(t *testing.T) {
	alpha := []rune{'a', 'b', 'c'}
	strs := [][]rune{{}}
	front := [][]rune{{}}
	for n := 1; n <= 5; n++ {
		var nf [][]rune
		for _, p := range front {
			for _, r := range alpha {
				q := append(append([]rune{}, p...), r)
				nf = append(nf, q)
				strs = append(strs, q)
			}
		}
		front = nf
	}
	for _, sa := range strs {
		for _, sb := range strs {
			a, b := string(sa), string(sb)
			d, _ := dist.Distance(a, b, 1<<62)
			sc, err := Script(a, b)
			if err != nil || Cost(sc) != d {
				t.Fatalf("%q->%q cost=%d d=%d err=%v", a, b, Cost(sc), d, err)
			}
			if got := Apply(a, sc); !runesEqual(dist.DecodeRunes(got), sb) {
				t.Fatalf("apply %q->%q got %q", a, b, got)
			}
			if err := validAdjacency(a, sc); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDeterminism(t *testing.T) {
	cases := [][2]string{
		{"CA", "ABC"}, {"kitten", "sitting"}, {"abcdef", "fedcba"},
		{"aabbcc", "abcabc"}, {"日本語", "語日本"}, {"", "abc"},
	}
	for _, c := range cases {
		first, _ := Script(c[0], c[1])
		for n := 0; n < 100; n++ {
			sc, _ := Script(c[0], c[1])
			if len(sc) != len(first) {
				t.Fatalf("%q->%q length changed", c[0], c[1])
			}
			for i := range sc {
				if sc[i] != first[i] {
					t.Fatalf("%q->%q op %d changed: %+v vs %+v",
						c[0], c[1], i, sc[i], first[i])
				}
			}
		}
	}
}

// validAdjacency re-simulates the script and asserts every Transpose swaps the
// two runes that are adjacent in the string at that moment.
func validAdjacency(a string, sc []Op) error {
	s := dist.DecodeRunes(a)
	c := 0
	for _, op := range sc {
		switch op.Kind {
		case Match:
			c++
		case Replace:
			s[c] = op.Y
			c++
		case Delete:
			if op.relTail {
				if s[c+1] != op.X {
					return adjErr(op, s, c)
				}
				s = append(s[:c+1], s[c+2:]...)
			} else {
				if s[c] != op.X {
					return adjErr(op, s, c)
				}
				s = append(s[:c], s[c+1:]...)
			}
		case Insert:
			s = append(s[:c], append([]rune{op.Y}, s[c:]...)...)
			c++
		case Transpose:
			if c+1 >= len(s) || s[c] != op.X || s[c+1] != op.Y {
				return adjErr(op, s, c)
			}
			s[c], s[c+1] = s[c+1], s[c]
		}
	}
	return nil
}

func adjErr(op Op, s []rune, c int) error {
	return &adjError{op: op, cur: string(s), pos: c}
}

type adjError struct {
	op  Op
	cur string
	pos int
}

func (e *adjError) Error() string {
	return "align: operation invalid on current string"
}
