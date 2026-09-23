// Package align reconstructs an optimal edit script from a dist DP
// table and can apply it to the source string.
package align

import "ontology/dist"

// Op is one edit operation. Positions refer to the evolving string:
// Src is the original source index of the affected character.
type Op struct {
	Kind  dist.Op
	Src   int    // affected source char index (replace/delete/transpose)
	Src2  int    // second swapped source index (transpose)
	Rune  int32  // inserted or replacement code point
	Next  int    // insert before original source index Next (len(a) = append)
}

// Script returns an optimal edit script transforming a into b.
// Its length equals dist(a,b). Ties resolve match/replace > delete >
// insert > transpose, matching the dist fill order.
func Script(a, b string, limit int64) ([]Op, error) {
	c := dist.Calculator{Limit: limit}
	r, err := c.Edit(a, b)
	if err != nil {
		return nil, err
	}

	var ops []Op
	i, j := len(r.A), len(r.B)
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && r.Op[i][j] == dist.OpTranspose:
			da, db := r.TI[i][j], r.TJ[i][j]
			for k := da; k <= i-2; k++ { // delete intervening source chars
				ops = append(ops, Op{Kind: dist.OpDelete, Src: k})
			}
			ops = append(ops, Op{Kind: dist.OpTranspose, Src: da - 1, Src2: i - 1})
			for k := db; k <= j-2; k++ { // insert intervening target chars
				ops = append(ops, Op{Kind: dist.OpInsert, Rune: r.B[k], Next: da - 1})
			}
			i, j = da-1, db-1
		case i > 0 && j > 0 && (r.Op[i][j] == dist.OpMatch || r.Op[i][j] == dist.OpReplace):
			ops = append(ops, Op{Kind: r.Op[i][j], Src: i - 1, Rune: r.B[j-1]})
			i--
			j--
		case j > 0 && (i == 0 || r.Op[i][j] == dist.OpInsert):
			ops = append(ops, Op{Kind: dist.OpInsert, Rune: r.B[j-1], Next: i})
			j--
		default:
			ops = append(ops, Op{Kind: dist.OpDelete, Src: i - 1})
			i--
		}
	}
	for l, rr := 0, len(ops)-1; l < rr; l, rr = l+1, rr-1 {
		ops[l], ops[rr] = ops[rr], ops[l]
	}
	return ops, nil
}

type token struct {
	tag  int // original source index, -1 for inserted
	rune int32
}

// Apply runs the script over a, code point by code point.
func Apply(a string, script []Op) string {
	src := dist.Decode(a)
	work := make([]token, len(src))
	for k, r := range src {
		work[k] = token{tag: k, rune: r}
	}
	insTag := -1
	for _, op := range script {
		switch op.Kind {
		case dist.OpMatch:
		case dist.OpReplace:
			for k := range work {
				if work[k].tag == op.Src {
					work[k].rune = op.Rune
				}
			}
		case dist.OpDelete:
			for k := range work {
				if work[k].tag == op.Src {
					work = append(work[:k], work[k+1:]...)
					break
				}
			}
		case dist.OpTranspose:
			x, y := -1, -1
			for k := range work {
				switch work[k].tag {
				case op.Src:
					x = k
				case op.Src2:
					y = k
				}
			}
			if x >= 0 && y >= 0 {
				work[x], work[y] = work[y], work[x]
			}
		case dist.OpInsert:
			pos := len(work)
			for k := range work {
				if work[k].tag >= op.Next && work[k].tag >= 0 {
					pos = k
					break
				}
			}
			tok := token{tag: insTag, rune: op.Rune}
			insTag--
			work = append(work, token{})
			copy(work[pos+1:], work[pos:])
			work[pos] = tok
		}
	}
	out := make([]int32, len(work))
	for k, t := range work {
		out[k] = t.rune
	}
	return dist.Encode(out)
}
