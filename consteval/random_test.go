package consteval

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// This file cross-checks the evaluator against an independent naive model
// built directly on big.Int / big.Rat, over randomly generated untyped
// expression trees. Every case logs its input, both outputs, and the
// verdict with its basis (run go test -v to see the log).

// --- naive model ---------------------------------------------------------

type naiveVal struct {
	isRat bool
	i     *big.Int
	r     *big.Rat
}

func (v naiveVal) rat() *big.Rat {
	if v.isRat {
		return new(big.Rat).Set(v.r)
	}
	return new(big.Rat).SetInt(v.i)
}

type naiveErr int

const (
	naiveOK naiveErr = iota
	naiveDivZero
	naiveTooLarge
)

func (e naiveErr) String() string {
	switch e {
	case naiveOK:
		return "ok"
	case naiveDivZero:
		return "division by zero"
	case naiveTooLarge:
		return "constant too large"
	}
	return "?"
}

func naiveInt(i *big.Int) (naiveVal, naiveErr) {
	if i.BitLen() > 512 {
		return naiveVal{}, naiveTooLarge
	}
	return naiveVal{i: i}, naiveOK
}

// naiveEval mirrors the mandated order: children left to right, then the
// parent; division by zero before the 512-bit magnitude check.
func naiveEval(e *Expr) (naiveVal, naiveErr) {
	switch e.Node {
	case LitInt:
		i, _ := new(big.Int).SetString(e.Int, 10)
		return naiveInt(i)
	case LitRat:
		r, _ := new(big.Rat).SetString(e.Rat)
		return naiveVal{isRat: true, r: r}, naiveOK
	case Unary:
		x, err := naiveEval(e.Args[0])
		if err != naiveOK {
			return naiveVal{}, err
		}
		if e.Op == OpBitNot {
			if x.isRat {
				panic("unreachable: bitnot on rat")
			}
			return naiveInt(new(big.Int).Not(x.i))
		}
		if x.isRat {
			return naiveVal{isRat: true, r: new(big.Rat).Neg(x.r)}, naiveOK
		}
		return naiveInt(new(big.Int).Neg(x.i))
	case Binary:
		l, err := naiveEval(e.Args[0])
		if err != naiveOK {
			return naiveVal{}, err
		}
		r, err := naiveEval(e.Args[1])
		if err != naiveOK {
			return naiveVal{}, err
		}
		return naiveBinary(e.Op, l, r)
	}
	panic("unreachable")
}

func naiveBinary(op Op, l, r naiveVal) (naiveVal, naiveErr) {
	if l.isRat || r.isRat {
		a, b := l.rat(), r.rat()
		switch op {
		case OpAdd:
			return naiveVal{isRat: true, r: new(big.Rat).Add(a, b)}, naiveOK
		case OpSub:
			return naiveVal{isRat: true, r: new(big.Rat).Sub(a, b)}, naiveOK
		case OpMul:
			return naiveVal{isRat: true, r: new(big.Rat).Mul(a, b)}, naiveOK
		case OpQuo:
			if b.Sign() == 0 {
				return naiveVal{}, naiveDivZero
			}
			return naiveVal{isRat: true, r: new(big.Rat).Quo(a, b)}, naiveOK
		}
		panic("unreachable: rat operand with int-only op")
	}
	a, b := l.i, r.i
	switch op {
	case OpAdd:
		return naiveInt(new(big.Int).Add(a, b))
	case OpSub:
		return naiveInt(new(big.Int).Sub(a, b))
	case OpMul:
		return naiveInt(new(big.Int).Mul(a, b))
	case OpQuo:
		if b.Sign() == 0 {
			return naiveVal{}, naiveDivZero
		}
		return naiveInt(new(big.Int).Quo(a, b))
	case OpRem:
		if b.Sign() == 0 {
			return naiveVal{}, naiveDivZero
		}
		return naiveInt(new(big.Int).Rem(a, b))
	case OpAnd:
		return naiveInt(new(big.Int).And(a, b))
	case OpOr:
		return naiveInt(new(big.Int).Or(a, b))
	case OpXor:
		return naiveInt(new(big.Int).Xor(a, b))
	case OpShl:
		return naiveInt(new(big.Int).Lsh(a, uint(b.Int64())))
	case OpShr:
		return naiveInt(new(big.Int).Rsh(a, uint(b.Int64())))
	}
	panic("unreachable")
}

// --- random tree generator ----------------------------------------------

func render(e *Expr) string {
	switch e.Node {
	case LitInt:
		return e.Int
	case LitRat:
		return "(" + e.Rat + ")"
	case Unary:
		return fmt.Sprintf("(%s %s)", e.Op, render(e.Args[0]))
	case Binary:
		return fmt.Sprintf("(%s %s %s)", e.Op, render(e.Args[0]), render(e.Args[1]))
	}
	return "?"
}

func genIntLeaf(r *rand.Rand) *Expr {
	switch r.Intn(10) {
	case 0:
		return IntLit("0")
	case 1, 2:
		// Large magnitudes to exercise the 512-bit boundary.
		bits := []uint{100, 300, 500, 511}[r.Intn(4)]
		s := new(big.Int).Lsh(big.NewInt(1), bits).String()
		if r.Intn(2) == 0 {
			s = "-" + s
		}
		return IntLit(s)
	default:
		return IntLit(fmt.Sprint(r.Int63n(200) - 100))
	}
}

// genIntTree generates integer-kind trees (all ops preserve the int kind).
func genIntTree(r *rand.Rand, depth int) *Expr {
	if depth == 0 {
		return genIntLeaf(r)
	}
	switch r.Intn(12) {
	case 0:
		return Neg(genIntTree(r, depth-1))
	case 1:
		return BitNot(genIntTree(r, depth-1))
	case 2:
		return Shl(genIntTree(r, depth-1), IntLit(fmt.Sprint(r.Intn(21))))
	case 3:
		return Shr(genIntTree(r, depth-1), IntLit(fmt.Sprint(r.Intn(21))))
	default:
		op := []Op{OpAdd, OpSub, OpMul, OpQuo, OpRem, OpAnd, OpOr, OpXor}[r.Intn(8)]
		return Bin(op, genIntTree(r, depth-1), genIntTree(r, depth-1))
	}
}

// genNumTree generates numeric trees mixing integer and rational kinds.
func genNumTree(r *rand.Rand, depth int) *Expr {
	if depth == 0 {
		if r.Intn(2) == 0 {
			return genIntLeaf(r)
		}
		den := 1 + r.Int63n(50)
		return RatLit(fmt.Sprintf("%d/%d", r.Int63n(200)-100, den))
	}
	if r.Intn(5) == 0 {
		return Neg(genNumTree(r, depth-1))
	}
	op := []Op{OpAdd, OpSub, OpMul, OpQuo}[r.Intn(4)]
	return Bin(op, genNumTree(r, depth-1), genNumTree(r, depth-1))
}

// --- cross-check ---------------------------------------------------------

func TestRandomExpressionsAgainstNaiveModel(t *testing.T) {
	r := rand.New(rand.NewSource(1602))
	const cases = 3000
	stats := map[string]int{}
	for n := 0; n < cases; n++ {
		var e *Expr
		if n%2 == 0 {
			e = genIntTree(r, 1+r.Intn(5))
		} else {
			e = genNumTree(r, 1+r.Intn(5))
		}
		got, gotErr := Eval(e, nil)
		want, wantErr := naiveEval(e)

		input := render(e)
		var gotDesc, wantDesc, verdict string
		switch {
		case gotErr == nil:
			gotDesc = got.String()
		default:
			gotDesc = gotErr.Error()
		}
		if wantErr == naiveOK {
			wantDesc = want.rat().RatString()
			if !want.isRat {
				wantDesc = "int " + want.i.String()
			} else {
				wantDesc = "rat " + wantDesc
			}
		} else {
			wantDesc = wantErr.String()
		}

		fail := false
		switch wantErr {
		case naiveOK:
			if gotErr != nil {
				fail = true
				verdict = "MISMATCH: naive model succeeds but evaluator errors"
			} else if (got.KindOf() == RatKind) != want.isRat {
				fail = true
				verdict = "MISMATCH: result kind differs"
			} else if got.Rat().Cmp(want.rat()) != 0 {
				fail = true
				verdict = "MISMATCH: exact value differs"
			} else {
				verdict = "agree: exact value and kind equal"
			}
			stats["value"]++
		case naiveDivZero:
			if errKind(t, gotErr) != ErrDivByZero {
				fail = true
				verdict = "MISMATCH: want division by zero"
			} else {
				verdict = "agree: division by zero"
			}
			stats["divzero"]++
		case naiveTooLarge:
			if errKind(t, gotErr) != ErrConstTooLarge {
				fail = true
				verdict = "MISMATCH: want constant too large"
			} else {
				verdict = "agree: constant too large (>512 bits)"
			}
			stats["toolarge"]++
		}
		t.Logf("case %d: input=%s\n  evaluator=%s\n  naive=%s\n  verdict=%s",
			n, input, gotDesc, wantDesc, verdict)
		if fail {
			t.Fatalf("case %d %s\ninput: %s\nevaluator: %s\nnaive: %s",
				n, verdict, input, gotDesc, wantDesc)
		}
	}
	t.Logf("cross-check summary over %d cases: %v", cases, stats)
}
