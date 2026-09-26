package tac

import (
	"fmt"

	"ontology/ast"
)

// gen is the per-call translation state; nothing is shared between calls.
// err latches the first rejection; Gen then discards the partial sequence.
type gen struct {
	code     []Instr
	tmp, lbl int
	temps    map[string]bool
	live     map[string]bool
	nlive    int
	peak     int // non-exported: peak of simultaneously live temps
	err      error
}

// Gen translates n to TAC; on rejection it returns nil and a sentinel error.
func Gen(n *ast.Expr) ([]Instr, error) {
	g := &gen{temps: map[string]bool{}, live: map[string]bool{}}
	g.materialize(g.expr(n)) // whole-expression result must land in a temp
	if g.err != nil {
		return nil, g.err
	}
	return g.code, nil
}

func (g *gen) emit(in Instr) {
	for _, o := range []string{in.A, in.B} {
		if g.live[o] {
			delete(g.live, o)
			g.nlive--
		}
	}
	if in.Dst != "" && !g.live[in.Dst] {
		g.live[in.Dst] = true
		g.nlive++
		g.peak = max(g.peak, g.nlive)
	}
	g.code = append(g.code, in)
}

func (g *gen) newTemp() string {
	g.tmp++
	t := fmt.Sprintf("t%d", g.tmp)
	g.temps[t] = true
	return t
}

func (g *gen) newLabel() string { g.lbl++; return fmt.Sprintf("L%d", g.lbl) }

// materialize ensures operand op sits in a temp, copying a variable if needed.
func (g *gen) materialize(op string) string {
	if g.err != nil || g.temps[op] {
		return op
	}
	t := g.newTemp()
	g.emit(Instr{Op: OpCopy, Dst: t, A: op})
	return t
}

// expr emits code for n and returns the operand (variable or temp) holding
// its value. Literals materialize themselves; variables stay as names.
func (g *gen) expr(n *ast.Expr) string {
	if g.err != nil {
		return ""
	}
	if n == nil {
		g.err = ErrNilNode
		return ""
	}
	switch n.Kind {
	case ast.KInt, ast.KBool:
		t := g.newTemp()
		if n.Kind == ast.KInt {
			g.emit(Instr{Op: OpConstInt, Dst: t, Imm: n.Int})
		} else {
			g.emit(Instr{Op: OpConstBool, Dst: t, ImmB: n.Bool})
		}
		return t
	case ast.KVar:
		return n.Name
	case ast.KBin:
		if !binOps[n.Op] {
			g.err = ErrUnknownOp
			return ""
		}
		l, r := g.expr(n.L), g.expr(n.R) // left before right
		t := g.newTemp()
		g.emit(Instr{Op: OpBin, Dst: t, A: l, BinOp: n.Op, B: r})
		return t
	case ast.KNot, ast.KNeg:
		a := g.expr(n.L)
		t := g.newTemp()
		op := OpNeg
		if n.Kind == ast.KNot {
			op = OpNot
		}
		g.emit(Instr{Op: op, Dst: t, A: a})
		return t
	case ast.KLogic:
		return g.logic(n)
	case ast.KIf:
		return g.ifElse(n)
	}
	g.err = ErrUnknownOp
	return ""
}

// logic emits the fixed short-circuit pattern; the right side's code sits
// between the conditional jump and its label, never executing when skipped.
func (g *gen) logic(n *ast.Expr) string {
	if n.Op != "&&" && n.Op != "||" {
		g.err = ErrUnknownOp
		return ""
	}
	lt := g.materialize(g.expr(n.L))
	done := g.newLabel()
	jump := OpIfFalse
	if n.Op == "||" {
		jump = OpIfTrue
	}
	g.emit(Instr{Op: jump, A: lt, Label: done})
	g.emit(Instr{Op: OpCopy, Dst: lt, A: g.materialize(g.expr(n.R))})
	g.emit(Instr{Op: OpLabel, Label: done})
	return lt
}

// ifElse evaluates both branches into one shared merge temp.
func (g *gen) ifElse(n *ast.Expr) string {
	if n.T == nil || n.E == nil {
		g.err = ErrMissingBranch
		return ""
	}
	ct := g.materialize(g.expr(n.C))
	els, end := g.newLabel(), g.newLabel()
	g.emit(Instr{Op: OpIfFalse, A: ct, Label: els})
	then := g.materialize(g.expr(n.T))
	res := g.newTemp()
	g.emit(Instr{Op: OpCopy, Dst: res, A: then})
	g.emit(Instr{Op: OpGoto, Label: end})
	g.emit(Instr{Op: OpLabel, Label: els})
	g.emit(Instr{Op: OpCopy, Dst: res, A: g.materialize(g.expr(n.E))})
	g.emit(Instr{Op: OpLabel, Label: end})
	return res
}
