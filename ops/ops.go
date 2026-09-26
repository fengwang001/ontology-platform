// Package ops backtracks a minimal edit script from the banded DP.
package ops

import (
	"errors"

	"ontology/dist"
)

// ErrInvalidScript reports a script that does not replay cleanly on a.
var ErrInvalidScript = errors.New("ops: script does not apply to input")

// OpKind identifies the atomic edit operation.
type OpKind int

const (
	OpInsert  OpKind = iota // insert Y
	OpDelete                // delete X
	OpReplace               // replace X with Y
)

// Op is one atomic edit at position Pos of a (for insert: before Pos).
// X is the byte consumed from a (delete/replace), Y the byte emitted.
type Op struct {
	Kind OpKind
	Pos  int
	X, Y byte
}

// EditScript returns a minimal script turning a into b (its length equals the
// edit distance), or dist.ErrExceedsCap when the distance exceeds k.
func EditScript(a, b string, k int) ([]Op, error) {
	d, err := dist.Distance(a, b, k)
	if err != nil {
		return nil, err
	}
	n, m := len(a), len(b)
	inf := k + 1
	// Banded table: row i stores j in [max(0,i-k), min(m,i+k)].
	rows := make([][]int, n+1)
	at := func(i, j int) int {
		lo := i - k
		if lo < 0 {
			lo = 0
		}
		if j < lo || j-lo >= len(rows[i]) {
			return inf
		}
		return rows[i][j-lo]
	}
	for i := 0; i <= n; i++ {
		lo, hi := i-k, i+k
		if lo < 0 {
			lo = 0
		}
		if hi > m {
			hi = m
		}
		rows[i] = make([]int, hi-lo+1)
		for j := lo; j <= hi; j++ {
			switch {
			case i == 0:
				rows[i][j-lo] = j
			case j == 0:
				rows[i][j-lo] = i
			default:
				best := at(i-1, j) + 1
				if v := at(i, j-1) + 1; v < best {
					best = v
				}
				cost := 1
				if a[i-1] == b[j-1] {
					cost = 0
				}
				if v := at(i-1, j-1) + cost; v < best {
					best = v
				}
				rows[i][j-lo] = best
			}
		}
	}
	// Backtrack from (n, m); every step follows an equality with an optimal
	// predecessor, so the script length is exactly d.
	rev := make([]Op, 0, d)
	for i, j := n, m; i > 0 || j > 0; {
		if i > 0 && j > 0 {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			if at(i, j) == at(i-1, j-1)+cost {
				if cost == 1 {
					rev = append(rev, Op{OpReplace, i - 1, a[i-1], b[j-1]})
				}
				i, j = i-1, j-1
				continue
			}
		}
		if j > 0 && at(i, j) == at(i, j-1)+1 {
			rev = append(rev, Op{OpInsert, i, 0, b[j-1]})
			j--
		} else {
			rev = append(rev, Op{OpDelete, i - 1, a[i-1], 0})
			i--
		}
	}
	script := make([]Op, len(rev))
	for i, op := range rev {
		script[len(rev)-1-i] = op
	}
	return script, nil
}

// Apply replays script on a, returning the transformed string. Bytes of a
// not covered by any op are copied through unchanged.
func Apply(a string, script []Op) (string, error) {
	out := make([]byte, 0, len(a)+len(script))
	cur := 0
	for _, op := range script {
		if op.Pos < cur || op.Pos > len(a) {
			return "", ErrInvalidScript
		}
		out = append(out, a[cur:op.Pos]...)
		cur = op.Pos
		switch op.Kind {
		case OpInsert:
			out = append(out, op.Y)
		case OpDelete, OpReplace:
			if cur >= len(a) || a[cur] != op.X {
				return "", ErrInvalidScript
			}
			if op.Kind == OpReplace {
				out = append(out, op.Y)
			}
			cur++
		default:
			return "", ErrInvalidScript
		}
	}
	return string(append(out, a[cur:]...)), nil
}
