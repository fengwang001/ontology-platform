package consteval

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Independent naive model: a straightforward recursive evaluator over
// big.Int / big.Rat, written separately from the implementation under
// test. Random untyped expressions are evaluated by both and must agree
// on value, kind and error kind.
// ---------------------------------------------------------------------------

type naiveVal struct {
	kind Kind
	rat  *big.Rat // numeric value (int kind uses integral rationals)
	b    bool
}

func naiveInt(v *big.Int) naiveVal { return naiveVal{kind: KindInt, rat: new(big.Rat).SetInt(v)} }
func naiveRat(v *big.Rat) naiveVal { return naiveVal{kind: KindRational, rat: v} }
func naiveBool(v bool) naiveVal    { return naiveVal{kind: KindBool, b: v} }

// naiveEval evaluates an untyped expression tree. It returns the value
// or an error kind, applying the same evaluation order (left to right,
// children first) and per-node priority (division by zero before
// constant-too-large).
func naiveEval(n *Node) (naiveVal, ErrKind, bool) {
	switch n.Op {
	case OpLit:
		switch n.Lit.Kind {
		case KindInt:
			if n.Lit.Int.BitLen() > 512 {
				return naiveVal{}, ErrTooLarge, true
			}
			return naiveInt(n.Lit.Int), 0, false
		case KindRational:
			return naiveRat(new(big.Rat).Set(n.Lit.Rat)), 0, false
		case KindBool:
			return naiveBool(n.Lit.Bool), 0, false
		}
	case OpNeg:
		a, k, bad := naiveEval(n.Args[0])
		if bad {
			return naiveVal{}, k, true
		}
		v := new(big.Rat).Neg(a.rat)
		if a.kind == KindInt {
			if v.Num().BitLen() > 512 {
				return naiveVal{}, ErrTooLarge, true
			}
			return naiveVal{kind: KindInt, rat: v}, 0, false
		}
		return naiveRat(v), 0, false
	case OpNot:
		a, k, bad := naiveEval(n.Args[0])
		if bad {
			return naiveVal{}, k, true
		}
		return naiveBool(!a.b), 0, false
	}

	l, k, bad := naiveEval(n.Args[0])
	if bad {
		return naiveVal{}, k, true
	}
	r, k, bad := naiveEval(n.Args[1])
	if bad {
		return naiveVal{}, k, true
	}

	// Division by zero outranks constant-too-large at the same node.
	if (n.Op == OpDiv || n.Op == OpMod) && r.rat.Sign() == 0 {
		return naiveVal{}, ErrDivByZero, true
	}

	switch n.Op {
	case OpAdd, OpSub, OpMul:
		v := new(big.Rat)
		switch n.Op {
		case OpAdd:
			v.Add(l.rat, r.rat)
		case OpSub:
			v.Sub(l.rat, r.rat)
		case OpMul:
			v.Mul(l.rat, r.rat)
		}
		if l.kind == KindInt && r.kind == KindInt {
			if v.Num().BitLen() > 512 {
				return naiveVal{}, ErrTooLarge, true
			}
			return naiveVal{kind: KindInt, rat: v}, 0, false
		}
		return naiveRat(v), 0, false
	case OpDiv:
		if l.kind == KindInt && r.kind == KindInt {
			// Truncated toward zero.
			q := new(big.Int).Quo(l.rat.Num(), r.rat.Num())
			return naiveInt(q), 0, false
		}
		return naiveRat(new(big.Rat).Quo(l.rat, r.rat)), 0, false
	case OpMod:
		m := new(big.Int).Rem(l.rat.Num(), r.rat.Num())
		return naiveInt(m), 0, false
	case OpBitAnd, OpBitOr, OpBitXor:
		x, y := l.rat.Num(), r.rat.Num()
		z := new(big.Int)
		switch n.Op {
		case OpBitAnd:
			z.And(x, y)
		case OpBitOr:
			z.Or(x, y)
		case OpBitXor:
			z.Xor(x, y)
		}
		if z.BitLen() > 512 {
			return naiveVal{}, ErrTooLarge, true
		}
		return naiveInt(z), 0, false
	case OpShl, OpShr:
		count := r.rat.Num()
		if count.Sign() < 0 || count.Cmp(big.NewInt(1000)) > 0 {
			return naiveVal{}, ErrIllegalOp, true
		}
		z := new(big.Int)
		if n.Op == OpShl {
			z.Lsh(l.rat.Num(), uint(count.Uint64()))
		} else {
			z.Rsh(l.rat.Num(), uint(count.Uint64()))
		}
		if z.BitLen() > 512 {
			return naiveVal{}, ErrTooLarge, true
		}
		return naiveInt(z), 0, false
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		return naiveBool(compareBigInts(n.Op, l.rat.Cmp(r.rat))), 0, false
	case OpLogAnd:
		return naiveBool(l.b && r.b), 0, false
	case OpLogOr:
		return naiveBool(l.b || r.b), 0, false
	}
	return naiveVal{}, ErrInvalidArgument, true
}

// ---------------------------------------------------------------------------
// Random tree generator (untyped expressions only).
// ---------------------------------------------------------------------------

type treeGen struct{ rng *rand.Rand }

func (g *treeGen) intLiteral() *Node {
	var v *big.Int
	switch g.rng.Intn(10) {
	case 0: // near the 512-bit limit, sometimes beyond
		bits := 500 + g.rng.Intn(25)
		v = new(big.Int).Rand(g.rng, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
	case 1: // small, including zero
		v = big.NewInt(int64(g.rng.Intn(5) - 1))
	default:
		v = big.NewInt(int64(g.rng.Intn(2000) - 1000))
	}
	if g.rng.Intn(4) == 0 {
		v.Neg(v)
	}
	return BigIntLit(v)
}

func (g *treeGen) ratLiteral() *Node {
	num := big.NewInt(int64(g.rng.Intn(200) - 100))
	den := big.NewInt(int64(g.rng.Intn(20) + 1))
	return &Node{Op: OpLit, Lit: Lit{Kind: KindRational, Rat: new(big.Rat).SetFrac(num, den)}}
}

// intKind builds a tree that always evaluates to the integer kind.
func (g *treeGen) intKind(depth int) *Node {
	if depth <= 0 {
		return g.intLiteral()
	}
	switch g.rng.Intn(10) {
	case 0:
		return Un(OpNeg, g.intKind(depth-1))
	case 1:
		return Bin(OpMod, g.intKind(depth-1), g.intKind(depth-1))
	case 2:
		return Bin(OpBitAnd, g.intKind(depth-1), g.intKind(depth-1))
	case 3:
		return Bin(OpBitOr, g.intKind(depth-1), g.intKind(depth-1))
	case 4:
		return Bin(OpBitXor, g.intKind(depth-1), g.intKind(depth-1))
	case 5:
		// Shift counts: usually legal, occasionally out of range.
		var count *Node
		switch g.rng.Intn(8) {
		case 0:
			count = IntLit("1001")
		case 1:
			count = IntLit("-1")
		default:
			count = IntLit(fmt.Sprint(g.rng.Intn(12)))
		}
		op := OpShl
		if g.rng.Intn(2) == 0 {
			op = OpShr
		}
		return Bin(op, g.intKind(depth-1), count)
	case 6:
		return Bin(OpDiv, g.intKind(depth-1), g.intKind(depth-1))
	default:
		op := []Op{OpAdd, OpSub, OpMul}[g.rng.Intn(3)]
		return Bin(op, g.intKind(depth-1), g.intKind(depth-1))
	}
}

// numeric builds a tree that evaluates to a numeric kind.
func (g *treeGen) numeric(depth int) *Node {
	if depth <= 0 {
		if g.rng.Intn(2) == 0 {
			return g.intLiteral()
		}
		return g.ratLiteral()
	}
	if g.rng.Intn(3) == 0 {
		return g.intKind(depth) // integer-only operators
	}
	switch g.rng.Intn(6) {
	case 0:
		return Un(OpNeg, g.numeric(depth-1))
	case 1:
		return Bin(OpDiv, g.numeric(depth-1), g.numeric(depth-1))
	default:
		op := []Op{OpAdd, OpSub, OpMul}[g.rng.Intn(3)]
		return Bin(op, g.numeric(depth-1), g.numeric(depth-1))
	}
}

// boolean builds a tree that evaluates to the bool kind.
func (g *treeGen) boolean(depth int) *Node {
	if depth <= 0 {
		if g.rng.Intn(2) == 0 {
			return BoolLit(g.rng.Intn(2) == 0)
		}
		op := []Op{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}[g.rng.Intn(6)]
		return Bin(op, g.numeric(1), g.numeric(1))
	}
	switch g.rng.Intn(4) {
	case 0:
		return Un(OpNot, g.boolean(depth-1))
	case 1:
		return Bin(OpLogAnd, g.boolean(depth-1), g.boolean(depth-1))
	case 2:
		return Bin(OpLogOr, g.boolean(depth-1), g.boolean(depth-1))
	default:
		op := []Op{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}[g.rng.Intn(6)]
		return Bin(op, g.numeric(depth-1), g.numeric(depth-1))
	}
}

func (g *treeGen) any() *Node {
	if g.rng.Intn(3) == 0 {
		return g.boolean(4)
	}
	return g.numeric(5)
}

// TestRandomAgainstNaiveModel cross-checks the evaluator against the
// independent naive model on many random untyped expressions, logging
// every input, output and the basis for the verdict.
func TestRandomAgainstNaiveModel(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("random seed: %d (rerun with the same seed to reproduce)", seed)
	rng := rand.New(rand.NewSource(seed))
	g := &treeGen{rng: rng}
	r := NewRegistry()

	const cases = 3000
	stats := map[ErrKind]int{}
	values := 0
	for i := 0; i < cases; i++ {
		expr := g.any()
		want, wantErr, hasErr := naiveEval(expr)
		got, err := r.Eval(expr)

		if hasErr {
			stats[wantErr]++
			if err == nil {
				t.Fatalf("case %d: input=%s\nreal=%s\nnaive=error %s", i, expr, got, wantErr)
			}
			if gotKind := errKindOf(err); gotKind != wantErr {
				t.Fatalf("case %d: input=%s\nreal error kind=%s (%v)\nnaive error kind=%s",
					i, expr, gotKind, err, wantErr)
			}
			t.Logf("case %d: input=%s output=%v basis=naive model agrees on error kind %s",
				i, expr, err, wantErr)
			continue
		}

		values++
		if err != nil {
			t.Fatalf("case %d: input=%s\nreal=error %v\nnaive=%s", i, expr, err, naiveString(want))
		}
		if got.Kind() != want.kind {
			t.Fatalf("case %d: input=%s\nreal kind=%s\nnaive kind=%s", i, expr, got.Kind(), want.kind)
		}
		switch want.kind {
		case KindInt, KindRational:
			if got.ratValue().Cmp(want.rat) != 0 {
				t.Fatalf("case %d: input=%s\nreal=%s\nnaive=%s", i, expr, got, naiveString(want))
			}
		case KindBool:
			if got.Bool() != want.b {
				t.Fatalf("case %d: input=%s\nreal=%s\nnaive=%s", i, expr, got, naiveString(want))
			}
		}
		t.Logf("case %d: input=%s output=%s basis=naive model agrees: kind=%s value=%s",
			i, expr, got, want.kind, naiveString(want))
	}
	t.Logf("summary: %d cases, %d values, errors by kind: %v", cases, values, stats)
}

func naiveString(v naiveVal) string {
	if v.kind == KindBool {
		return fmt.Sprintf("bool %t", v.b)
	}
	return fmt.Sprintf("%s %s", v.kind, v.rat.RatString())
}
