package dist

import "unicode/utf8"

const DefaultMaxProduct = 10_000_000

var ErrProductLimit = productLimitError{}

type productLimitError struct{}

func (productLimitError) Error() string { return "dist: rune-count product exceeds configured limit" }

const (
	kindMatch byte = iota
	kindSubstitute
	kindDelete
	kindInsert
	kindTranspose
)

// Codepoints splits s by codepoint. Each distinct invalid UTF-8 byte becomes a
// distinct surrogate rune in 0xD800..0xDBFF, so different bad bytes compare unequal.
func Codepoints(s string) []rune {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			rs = append(rs, 0xD800+rune(s[i]))
		} else {
			rs = append(rs, r)
		}
		i += size
	}
	return rs
}

// EncodeCodepoint is the inverse embedding used by Codepoints for invalid bytes.
func EncodeCodepoint(r rune) []byte {
	switch {
	case r >= 0xD800 && r <= 0xDBFF:
		return []byte{byte(0x80 + r - 0xD800)}
	default:
		buf := make([]byte, utf8.RuneLen(r))
		utf8.EncodeRune(buf, r)
		return buf
	}
}

// Table is the filled DP table with parent-pointer annotations for backtracking.
type Table struct {
	a, b     []rune
	d        [][]int
	kd       [][]byte
	filled   int
	anchor   map[int]int
}

func (t *Table) Rows() int { return len(t.a) + 1 }
func (t *Table) Cols() int { return len(t.b) + 1 }
func (t *Table) Cost(i, j int) int { return t.d[i][j] }
func (t *Table) KindAt(i, j int) byte { return t.kind(i, j) }
func (t *Table) Source() []rune { return t.a }
func (t *Table) Target() []rune { return t.b }
func (t *Table) Distance() int { return t.d[len(t.a)][len(t.b)] }

// Anchor returns the (i1,j1) anchor of a transpose decision at (i,j).
func (t *Table) Anchor(i, j int) (int, int) {
	v := t.anchor[i*len(t.d[0])+j]
	return v >> 16, v & 0xFFFF
}

func (t *Table) kind(i, j int) byte {
	return t.kd[i][j]
}

// Calculator computes unrestricted Damerau–Levenshtein distance.
type Calculator struct {
	MaxProduct int
	cells      int
}

func New() *Calculator { return &Calculator{MaxProduct: DefaultMaxProduct} }

func (c *Calculator) CellsFilled() int { return c.cells }

func (c *Calculator) Distance(x, y string) (int, error) {
	tb, err := c.Table(x, y)
	if err != nil {
		return 0, err
	}
	return tb.Distance(), nil
}

func (c *Calculator) Table(x, y string) (*Table, error) {
	a, b := Codepoints(x), Codepoints(y)
	if int64(len(a))*int64(len(b)) > int64(c.MaxProduct) {
		return nil, ErrProductLimit
	}
	t := &Table{a: a, b: b}
	t.d = make([][]int, len(a)+1)
	t.kd = make([][]byte, len(a)+1)
	for i := range t.d {
		t.d[i] = make([]int, len(b)+1)
		t.kd[i] = make([]byte, len(b)+1)
	}
	t.anchor = map[int]int{}
	t.fill()
	c.cells += t.filled
	return t, nil
}

func (t *Table) fill() {
	for i := 1; i < t.Rows(); i++ {
		t.d[i][0] = i
		t.kd[i][0] = kindDelete
	}
	for j := 1; j < t.Cols(); j++ {
		t.d[0][j] = j
		t.kd[0][j] = kindInsert
	}
	lastA := map[rune]int{}
	for i := 1; i < t.Rows(); i++ {
		ai := t.a[i-1]
		j1 := 0
		for j := 1; j < t.Cols(); j++ {
			bj := t.b[j-1]
			best := t.d[i-1][j] + 1
			kd := kindDelete
			if v := t.d[i][j-1] + 1; v < best {
				best, kd = v, kindInsert
			}
			diag := t.d[i-1][j-1]
			if ai != bj {
				diag++
			}
			if diag < best {
				best = diag
				if ai == bj {
					kd = kindMatch
				} else {
					kd = kindSubstitute
				}
			}
			i1 := lastA[bj]
			if i1 > 0 && j1 > 0 {
				tv := t.d[i1-1][j1-1] + (i-i1-1) + 1 + (j-j1-1)
				if tv < best {
					best, kd = tv, kindTranspose
					t.anchor[i*t.Cols()+j] = i1<<16 | j1
				}
			}
			t.d[i][j], t.kd[i][j] = best, kd
			if bj == ai {
				j1 = j
			}
			t.filled++
		}
		lastA[ai] = i
	}
}
