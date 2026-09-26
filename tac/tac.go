// Package tac lowers an ast.Expr into three-address-code instructions.
package tac

import (
	"errors"
	"fmt"
	"ontology/ast"
)

var ErrNilNode, ErrUnknownOp, ErrMissingBranch = errors.New("tac: nil AST node"), errors.New("tac: unknown operator"), errors.New("tac: if-then-else missing then or else branch")

type Op string

const OpConst, OpCopy, OpBinary, OpNeg, OpNot, OpJmpFalse, OpJmpTrue, OpGoto, OpLabel Op = "lit", "mov", "bin", "-", "!", "jz", "jnz", "goto", "lab"

type Instr struct {
	Op                         Op
	Result, A, B, BinOp, Label string
}

var (
	jmpEq    = map[Op]string{OpJmpFalse: "==", OpJmpTrue: "!="}
	shortJmp = map[string]Op{"&&": OpJmpFalse, "||": OpJmpTrue}
	binOps   = map[string]bool{"+": true, "-": true, "*": true, "/": true,
		"<": true, ">": true, "<=": true, ">=": true, "==": true, "!=": true}
)

func (i Instr) String() string {
	switch i.Op {
	case OpConst, OpCopy:
		return i.Result + " = " + i.A
	case OpBinary:
		return fmt.Sprintf("%s = %s %s %s", i.Result, i.A, i.BinOp, i.B)
	case OpNeg, OpNot:
		return i.Result + " = " + string(i.Op) + i.A
	case OpJmpFalse, OpJmpTrue:
		return fmt.Sprintf("if %s %s 0 goto %s", i.A, jmpEq[i.Op], i.Label)
	case OpGoto:
		return "goto " + i.Label
	default:
		return i.Label + ":"
	}
}

// Gen lowers n; on any error it returns nil and a sentinel error.
func Gen(n *ast.Expr) ([]Instr, error) {
	g := &generator{}
	g.gen(n)
	if g.err != nil {
		return nil, g.err
	}
	return g.out, nil
}

type generator struct {
	out            []Instr
	tmpN, labN     int
	live, peakLive int // unexported peak-live-temp counter
	err            error
}

func (g *generator) tmp() string {
	g.tmpN++
	g.live++
	g.peakLive = max(g.peakLive, g.live)
	return fmt.Sprintf("t%d", g.tmpN)
}
func (g *generator) lab() string      { g.labN++; return fmt.Sprintf("L%d", g.labN) }
func (g *generator) emit(xs ...Instr) { g.out = append(g.out, xs...) }
func (g *generator) use(n int)        { g.live -= n }

func (g *generator) gen(n *ast.Expr) string {
	if n == nil {
		g.err = ErrNilNode
		return ""
	}
	switch n.Kind {
	case ast.IntLit, ast.BoolLit:
		v := fmt.Sprintf("%d", n.Val)
		if n.Kind == ast.BoolLit {
			v = fmt.Sprintf("%t", n.BVal)
		}
		t := g.tmp()
		g.emit(Instr{Op: OpConst, Result: t, A: v})
		return t
	case ast.Var:
		t := g.tmp()
		g.emit(Instr{Op: OpCopy, Result: t, A: n.Name})
		return t
	case ast.Unary:
		if n.Op != "-" && n.Op != "!" {
			g.err = ErrUnknownOp
			return ""
		}
		a := g.gen(n.Left)
		g.use(1)
		t := g.tmp()
		g.emit(Instr{Op: Op(n.Op), Result: t, A: a})
		return t
	case ast.Binary:
		return g.genBinary(n)
	case ast.If:
		return g.genIf(n)
	}
	g.err = ErrNilNode
	return ""
}

func (g *generator) genBinary(n *ast.Expr) string {
	if j, ok := shortJmp[n.Op]; ok {
		l := g.gen(n.Left) // left slot is also the merged result slot
		end := g.lab()
		g.emit(Instr{Op: j, A: l, Label: end})
		r := g.gen(n.Right) // emitted only after the short-circuit jump
		g.emit(Instr{Op: OpCopy, Result: l, A: r}, Instr{Op: OpLabel, Label: end})
		g.use(1)
		return l
	}
	if !binOps[n.Op] {
		g.err = ErrUnknownOp
		return ""
	}
	l, r := g.gen(n.Left), g.gen(n.Right)
	g.use(2) // operands consumed at this instruction boundary
	t := g.tmp()
	g.emit(Instr{Op: OpBinary, Result: t, A: l, B: r, BinOp: n.Op})
	return t
}

func (g *generator) genIf(n *ast.Expr) string {
	if n.Left == nil || n.Right == nil {
		g.err = ErrMissingBranch
		return ""
	}
	c := g.gen(n.Cond)
	lElse := g.lab()
	g.emit(Instr{Op: OpJmpFalse, A: c, Label: lElse})
	g.use(1)
	th := g.gen(n.Left)
	g.use(1)
	join := g.tmp()
	g.emit(Instr{Op: OpCopy, Result: join, A: th})
	lEnd := g.lab()
	g.emit(Instr{Op: OpGoto, Label: lEnd}, Instr{Op: OpLabel, Label: lElse})
	el := g.gen(n.Right)
	g.emit(Instr{Op: OpCopy, Result: join, A: el}, Instr{Op: OpLabel, Label: lEnd})
	g.use(1)
	return join
}
