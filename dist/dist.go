// Package dist computes the unrestricted Damerau-Levenshtein distance
// between UTF-8 strings, compared code point by code point.
package dist

import (
	"errors"
	"unicode/utf8"
)

// ErrTooLarge is returned when the code-point product exceeds the
// configured limit. It is checked before any DP matrix is allocated.
var ErrTooLarge = errors.New("dist: code point product exceeds limit")

// InvalidBase encodes an invalid UTF-8 byte b as InvalidBase+b. Two
// different invalid bytes therefore become two different code points,
// while InvalidBase itself lies above the Unicode range.
const InvalidBase int32 = 0x110000

// Op identifies the edit used to reach a DP cell.
//
//go:generate stringer -type=Op
type Op uint8

const (
	OpNone Op = iota
	OpMatch
	OpReplace
	OpDelete
	OpInsert
	OpTranspose
)

// Result holds a filled DP table plus backtracking information.
type Result struct {
	D         [][]int    // distance table
	Op        [][]Op     // operation chosen for each cell
	TI, TJ    [][]int    // transpose anchor (da, db): swapped source/target chars
	A, B      []int32    // code points, invalid bytes encoded above InvalidBase
	Cells     int64      // number of DP cells filled
}

// Dist returns the final distance.
func (r *Result) Dist() int { return r.D[len(r.A)][len(r.B)] }

// Calculator computes distances. The zero value is ready for use.
// Limit, when positive, caps len(A)*len(B) in code points.
type Calculator struct {
	Limit int64
	cells int64
}

// Cells reports the DP cells filled by the most recent Edit call.
func (c *Calculator) Cells() int64 { return c.cells }

// Decode converts a string to code points; each invalid UTF-8 byte
// becomes a distinct value above InvalidBase.
func Decode(s string) []int32 {
	out := make([]int32, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, InvalidBase+int32(s[i]))
		} else {
			out = append(out, r)
		}
		i += size
	}
	return out
}

// Encode converts code points back to a UTF-8 string.
func Encode(p []int32) string {
	buf := make([]byte, 0, len(p))
	var tmp [utf8.UTFMax]byte
	for _, r := range p {
		if r >= InvalidBase {
			buf = append(buf, byte(r-InvalidBase))
			continue
		}
		n := utf8.EncodeRune(tmp[:], rune(r))
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

// Edit fills the DP table for a and b and returns the result.
func (c *Calculator) Edit(a, b string) (*Result, error) {
	A, B := Decode(a), Decode(b)
	m, n := len(A), len(B)
	if c.Limit > 0 && int64(m)*int64(n) > c.Limit {
		return nil, ErrTooLarge
	}

	D := make([][]int, m+1)
	op := make([][]Op, m+1)
	ti := make([][]int, m+1)
	tj := make([][]int, m+1)
	for i := range D {
		D[i] = make([]int, n+1)
		op[i] = make([]Op, n+1)
		ti[i] = make([]int, n+1)
		tj[i] = make([]int, n+1)
		D[i][0] = i
		if i > 0 {
			op[i][0] = OpDelete
		}
	}
	for j := 0; j <= n; j++ {
		D[0][j] = j
		if j > 0 {
			op[0][j] = OpInsert
		}
	}

	var cells int64 = int64(m + n + 1)
	lastRow := make(map[int32]int) // da: row of previous occurrence of B[j-1] in A
	for i := 1; i <= m; i++ {
		lastCol := make(map[int32]int) // db: column of previous occurrence of A[i-1] in B
		for j := 1; j <= n; j++ {
			cells++
			best := D[i-1][j-1]
			choice := OpReplace
			if A[i-1] == B[j-1] {
				best--
				choice = OpMatch
			}
			best++
			if d := D[i-1][j] + 1; d < best {
				best, choice = d, OpDelete
			}
			if v := D[i][j-1] + 1; v < best {
				best, choice = v, OpInsert
			}
			da, okDa := lastRow[B[j-1]]
			db, okDb := lastCol[A[i-1]]
			if okDa && okDb {
				tr := D[da-1][db-1] + (i-da-1) + 1 + (j-db-1)
				if tr < best {
					best, choice, ti[i][j], tj[i][j] = tr, OpTranspose, da, db
				}
			}
			D[i][j], op[i][j] = best, choice
			lastCol[B[j-1]] = j
		}
		lastRow[A[i-1]] = i
	}

	c.cells = cells
	return &Result{D: D, Op: op, TI: ti, TJ: tj, A: A, B: B, Cells: cells}, nil
}

// Distance is a convenience wrapper around Calculator.Edit.
func Distance(a, b string, limit int64) (int, error) {
	c := Calculator{Limit: limit}
	r, err := c.Edit(a, b)
	if err != nil {
		return 0, err
	}
	return r.Dist(), nil
}
