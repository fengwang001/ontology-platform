// Package tac lowers an ast.Expr to three-address code and provides a naive
// interpreter for the generated instructions. Depends on ast only.
package tac

import "errors"

// Rejection reasons, all mutually distinct sentinel errors.
var (
	ErrNilNode       = errors.New("tac: nil node")
	ErrUnknownOp     = errors.New("tac: unknown operator")
	ErrMissingBranch = errors.New("tac: if missing then/else branch")
)

var errDivZero = errors.New("tac: division by zero")

var binOps = map[string]bool{"+": true, "-": true, "*": true, "/": true,
	"<": true, ">": true, "<=": true, ">=": true, "==": true, "!=": true}

// Op is the instruction opcode.
type Op int

const (
	OpConstInt  Op = iota // Dst = Imm
	OpConstBool           // Dst = ImmB
	OpCopy                // Dst = A
	OpBin                 // Dst = A BinOp B
	OpNeg                 // Dst = -A
	OpNot                 // Dst = !A
	OpIfFalse             // if A == 0 goto Label
	OpIfTrue              // if A != 0 goto Label
	OpGoto                // goto Label
	OpLabel               // Label:
)

// Instr is one three-address instruction. Fields not implied by Op are zero.
type Instr struct {
	Op    Op
	Dst   string
	A     string
	B     string
	BinOp string
	Imm   int64
	ImmB  bool
	Label string
}

// Exec naively interprets code one instruction at a time; env maps variable
// names to values (booleans as 0/1). It returns the value of the temp written
// by the last value-producing instruction, which is where Gen places the
// whole-expression result.
func Exec(code []Instr, env map[string]int64) (int64, error) {
	labels := map[string]int{}
	vals := map[string]int64{}
	res := ""
	for i, in := range code {
		if in.Op == OpLabel {
			labels[in.Label] = i
		}
		if in.Dst != "" {
			res = in.Dst
		}
	}
	get := func(s string) int64 {
		if v, ok := vals[s]; ok {
			return v
		}
		return env[s]
	}
	for pc := 0; pc < len(code); {
		in := code[pc]
		pc++
		switch in.Op {
		case OpConstInt:
			vals[in.Dst] = in.Imm
		case OpConstBool:
			if in.ImmB {
				vals[in.Dst] = 1
			} else {
				vals[in.Dst] = 0
			}
		case OpCopy:
			vals[in.Dst] = get(in.A)
		case OpNeg:
			vals[in.Dst] = -get(in.A)
		case OpNot:
			if get(in.A) == 0 {
				vals[in.Dst] = 1
			} else {
				vals[in.Dst] = 0
			}
		case OpBin:
			a, b := get(in.A), get(in.B)
			if in.BinOp == "/" && b == 0 {
				return 0, errDivZero
			}
			vals[in.Dst] = arith(in.BinOp, a, b)
		case OpIfFalse:
			if get(in.A) == 0 {
				pc = labels[in.Label]
			}
		case OpIfTrue:
			if get(in.A) != 0 {
				pc = labels[in.Label]
			}
		case OpGoto:
			pc = labels[in.Label]
		}
	}
	return vals[res], nil
}

func arith(op string, a, b int64) int64 {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b
	case "<":
		return b2i(a < b)
	case ">":
		return b2i(a > b)
	case "<=":
		return b2i(a <= b)
	case ">=":
		return b2i(a >= b)
	case "==":
		return b2i(a == b)
	default:
		return b2i(a != b)
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
