// Package align backtracks an edit script from the dist DP table and can
// apply a script to the source string to obtain the target string.
package align

import (
	"errors"

	"ontology/dist"
)

// ErrPos reports an op whose position is invalid for the current string.
var ErrPos = errors.New("align: op position out of range")

// Kind is the edit operation type.
type Kind int

const (
	Del   Kind = iota // delete current[Pos]
	Ins               // insert Rune before current[Pos]
	Sub               // replace current[Pos] with Rune
	Trans             // swap the adjacent current[Pos], current[Pos+1]
)

// Op is one edit step. Pos refers to the string as it is when the op is
// applied; scripts are applied from right to left over the source.
type Op struct {
	Kind Kind
	Pos  int
	Rune rune
}

// Script returns an optimal edit script from a to b. Its length equals
// dist.Distance(a, b). Ties are broken by the fixed priority
// match > substitute > delete > insert > transpose.
func Script(a, b string) ([]Op, error) {
	ra, rb := dist.Decode(a), dist.Decode(b)
	d, err := dist.Table(a, b)
	if err != nil {
		return nil, err
	}
	var ops []Op
	i, j := len(ra), len(rb)
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && ra[i-1] == rb[j-1] && d[i][j] == d[i-1][j-1]:
			i, j = i-1, j-1
		case i > 0 && j > 0 && d[i][j] == d[i-1][j-1]+1:
			ops = append(ops, Op{Sub, i - 1, rb[j-1]})
			i, j = i-1, j-1
		case i > 0 && d[i][j] == d[i-1][j]+1:
			ops = append(ops, Op{Del, i - 1, 0})
			i--
		case j > 0 && d[i][j] == d[i][j-1]+1:
			ops = append(ops, Op{Ins, i, rb[j-1]})
			j--
		default: // transposition move from (i1-1, j1-1)
			i1 := lastBefore(ra, i, rb[j-1])
			j1 := lastBefore(rb, j, ra[i-1])
			for k := i - 2; k >= i1; k-- {
				ops = append(ops, Op{Del, k, 0})
			}
			ops = append(ops, Op{Trans, i1 - 1, 0})
			for k := j1; k <= j-2; k++ {
				ops = append(ops, Op{Ins, i1 + k - j1, rb[k]})
			}
			i, j = i1-1, j1-1
		}
	}
	return ops, nil
}

// lastBefore returns the largest k in [1, end-1] with s[k-1] == r, else 0.
func lastBefore(s []rune, end int, r rune) int {
	for k := end - 1; k >= 1; k-- {
		if s[k-1] == r {
			return k
		}
	}
	return 0
}

// Apply applies script to a and returns the resulting string. A Trans op
// always swaps two adjacent code points of the current string.
func Apply(a string, script []Op) (string, error) {
	cur := dist.Decode(a)
	for _, op := range script {
		switch op.Kind {
		case Del:
			if op.Pos < 0 || op.Pos >= len(cur) {
				return "", ErrPos
			}
			cur = append(cur[:op.Pos], cur[op.Pos+1:]...)
		case Ins:
			if op.Pos < 0 || op.Pos > len(cur) {
				return "", ErrPos
			}
			cur = append(cur, 0)
			copy(cur[op.Pos+1:], cur[op.Pos:])
			cur[op.Pos] = op.Rune
		case Sub:
			if op.Pos < 0 || op.Pos >= len(cur) {
				return "", ErrPos
			}
			cur[op.Pos] = op.Rune
		case Trans:
			if op.Pos < 0 || op.Pos+1 >= len(cur) {
				return "", ErrPos
			}
			cur[op.Pos], cur[op.Pos+1] = cur[op.Pos+1], cur[op.Pos]
		}
	}
	return dist.Encode(cur), nil
}
