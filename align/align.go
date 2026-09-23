package align

import (
	"strings"

	"ontology/dist"
)

// Kind identifies one edit operation applied at Pos in the current string.
type Kind byte

const (
	Substitute Kind = 1
	Delete     Kind = 2
	Insert     Kind = 3
	Transpose  Kind = 4
)

// Op is one edit. Pos is the codepoint index in the string as it exists at the
// moment the operation is applied. For Transpose it is the first of two
// adjacent codepoints; Rune is the inserted/substituted codepoint.
type Op struct {
	Kind Kind
	Pos  int
	Rune rune
}

// Script returns an optimal edit script transforming a into b.
func Script(a, b string) ([]Op, error) {
	return NewCalculator().Script(a, b)
}

// Calculator builds scripts with the same configurable limit as dist.Calculator.
type Calculator struct {
	Dist *dist.Calculator
}

func NewCalculator() Calculator {
	return Calculator{Dist: dist.New()}
}

type decision struct {
	i, j int
	kind byte
	i1   int
	j1   int
}

func (c Calculator) Script(a, b string) ([]Op, error) {
	t, err := c.Dist.Table(a, b)
	if err != nil {
		return nil, err
	}
	var rev []decision
	i, j := len(t.Source()), len(t.Target())
	for i > 0 || j > 0 {
		kd := t.KindAt(i, j)
		dec := decision{i: i, j: j, kind: kd}
		if kd == 4 {
			dec.i1, dec.j1 = t.Anchor(i, j)
			rev = append(rev, dec)
			i, j = dec.i1-1, dec.j1-1
			continue
		}
		rev = append(rev, dec)
		if kd <= 1 {
			i--
			j--
		} else if kd == 2 {
			i--
		} else {
			j--
		}
	}
	return emit(t.Source(), t.Target(), rev), nil
}

func emit(a, b []rune, rev []decision) []Op {
	var ops []Op
	cur := append([]rune(nil), a...)
	l := 0
	for n := len(rev) - 1; n >= 0; n-- {
		c := rev[n]
		switch c.kind {
		case 0:
			l++
		case 1:
			ops = append(ops, Op{Kind: Substitute, Pos: l, Rune: b[c.j-1]})
			cur[l] = b[c.j-1]
			l++
		case 2:
			ops = append(ops, Op{Kind: Delete, Pos: l})
			cur = append(cur[:l], cur[l+1:]...)
		case 3:
			ops = append(ops, Op{Kind: Insert, Pos: l, Rune: b[c.j-1]})
			cur = append(cur[:l], append([]rune{b[c.j-1]}, cur[l:]...)...)
			l++
		default:
			for p := c.i - 1; p > c.i1; p-- {
				ops = append(ops, Op{Kind: Delete, Pos: l})
				cur = append(cur[:l], cur[l+1:]...)
			}
			ops = append(ops, Op{Kind: Transpose, Pos: l})
			cur[l], cur[l+1] = cur[l+1], cur[l]
			for q := c.j1 + 1; q < c.j; q++ {
				pos := l + (q - c.j1)
				ops = append(ops, Op{Kind: Insert, Pos: pos, Rune: b[q-1]})
				cur = append(cur[:pos], append([]rune{b[q-1]}, cur[pos:]...)...)
			}
			l = c.j
		}
	}
	return ops
}

// Apply runs the script over a, codepoint by codepoint.
func Apply(a string, script []Op) string {
	cur := dist.Codepoints(a)
	for _, op := range script {
		switch op.Kind {
		case Substitute:
			cur[op.Pos] = op.Rune
		case Delete:
			cur = append(cur[:op.Pos], cur[op.Pos+1:]...)
		case Insert:
			cur = append(cur[:op.Pos], append([]rune{op.Rune}, cur[op.Pos:]...)...)
		case Transpose:
			cur[op.Pos], cur[op.Pos+1] = cur[op.Pos+1], cur[op.Pos]
		}
	}
	var sb strings.Builder
	for _, r := range cur {
		sb.Write(dist.EncodeCodepoint(r))
	}
	return sb.String()
}
