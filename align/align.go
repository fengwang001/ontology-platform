// Package align reconstructs an edit script from the unrestricted
// Damerau–Levenshtein computation in package dist and applies it.

// Package align reconstructs an edit script from the unrestricted
// Damerau–Levenshtein computation in package dist and applies it.
package align

import "ontology/dist"

// Kind identifies an edit operation.
type Kind uint8

// Operation kinds, each of unit cost except Match.
const (
	Match Kind = iota
	Replace
	Delete
	Insert
	Transpose
)

// Op is one edit step. X and Y carry the code points involved at the moment
// the step executes on the mutable string (Transpose: X,Y are the adjacent
// pair in their current order; Delete: X is the removed rune; Insert: Y is
// the inserted rune).
type Op struct {
	Kind    Kind
	X, Y    rune
	relTail bool // Delete: remove the rune just right of the cursor
}

// Script returns an optimal edit script transforming a into b. Its number of
// non-Match operations equals dist.Distance(a,b). Tie-breaking is fixed
// (match > replace > delete > insert > transpose), see DESIGN.md.
func Script(a, b string) ([]Op, error) {
	t, err := dist.Build(a, b, 1<<62)
	if err != nil {
		return nil, err
	}
	aa, bb := t.Runes()
	var rev []Op
	emit := func(o Op) { rev = append(rev, o) }
	i, j := len(aa)-1, len(bb)-1
	for i > 0 || j > 0 {
		src, k, l := t.Source(i, j)
		switch src {
		case dist.SrcMatch:
			emit(Op{Kind: Match, X: aa[i], Y: bb[j]})
			i--
			j--
		case dist.SrcReplace:
			emit(Op{Kind: Replace, X: aa[i], Y: bb[j]})
			i--
			j--
		case dist.SrcDelete:
			emit(Op{Kind: Delete, X: aa[i]})
			i--
		case dist.SrcInsert:
			emit(Op{Kind: Insert, Y: bb[j]})
			j--
		default: // transposition a[k]<->a[i], matched to b[l],b[j]
			// Emitted in reverse: forward order is tail-deletes, swap,
			// Match(a[k]), inserts, Match(a[i]). The swap leaves the
			// cursor before the pair, so inserts land between them.
			emit(Op{Kind: Match, X: aa[k], Y: bb[l]})
			for y := j - 1; y > l; y-- {
				emit(Op{Kind: Insert, Y: bb[y]})
			}
			emit(Op{Kind: Match, X: aa[i], Y: bb[j]})
			emit(Op{Kind: Transpose, X: aa[k], Y: aa[i]})
			for x := i - 1; x > k; x-- {
				emit(Op{Kind: Delete, X: aa[x], relTail: true})
			}
			i, j = k-1, l-1
		}
	}
	out := make([]Op, len(rev))
	for p, o := range rev {
		out[len(rev)-1-p] = o
	}
	return out, nil
}

// Apply runs script over a, starting at a code-point cursor, and returns the
// resulting string. Every Transpose swaps two currently adjacent code points.
func Apply(a string, script []Op) string {
	s := dist.DecodeRunes(a)
	c := 0 // code-point cursor
	for _, op := range script {
		switch op.Kind {
		case Match:
			c++
		case Replace:
			s[c] = op.Y
			c++
		case Delete:
			if op.relTail {
				s = append(s[:c+1], s[c+2:]...) // rune just right of cursor
			} else {
				s = append(s[:c], s[c+1:]...)
			}
		case Insert:
			s = append(s[:c], append([]rune{op.Y}, s[c:]...)...)
			c++
		case Transpose:
			s[c], s[c+1] = s[c+1], s[c]
		}
	}
	return dist.EncodeRunes(s)
}

// Cost counts the non-Match operations in script (unit cost each).
func Cost(script []Op) int {
	n := 0
	for _, op := range script {
		if op.Kind != Match {
			n++
		}
	}
	return n
}
