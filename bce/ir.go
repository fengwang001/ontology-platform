// Package bce implements a bounds-check elimination (BCE) subsystem.
//
// Input is control-flow IR (Program/Block/Instr); every subscript access
// is a Check instruction carrying a bounds check. The subsystem uses only
// four fact classes (constant index, constant array length, path
// comparison conditions, previously passed checks) to decide which checks
// can be removed, and reports objective reasons for kept checks.
package bce

import "fmt"

// Operand is a constant or a scalar variable.
type Operand struct {
	IsConst bool
	Const   int
	Var     string
}

func ConstOp(v int) Operand  { return Operand{IsConst: true, Const: v} }
func VarOp(v string) Operand { return Operand{Var: v} }

func (o Operand) String() string {
	if o.IsConst {
		return fmt.Sprintf("%d", o.Const)
	}
	return o.Var
}

// InstrKind classifies non-terminator instructions.
type InstrKind int

const (
	OpAssignConst InstrKind = iota // Dst = C
	OpAssign                       // Dst = Src (scalar copy)
	OpAssignAdd                    // Dst = Src + C
	OpNewArray                     // Arr = new array[C] (array reassign, new const len)
	OpAssignArray                  // Arr = Src (array copy, treated as reassign)
	OpCheck                        // bounds check: 0 <= Idx < len(Arr)
)

// Instr is a flat instruction; fields are used according to Kind.
type Instr struct {
	Kind InstrKind
	Dst  string
	Src  string
	C    int
	Arr  string
	Idx  Operand
	ID   int // check id, assigned during validation in declaration order
}

func AssignConst(dst string, v int) Instr { return Instr{Kind: OpAssignConst, Dst: dst, C: v} }
func Assign(dst, src string) Instr        { return Instr{Kind: OpAssign, Dst: dst, Src: src} }
func AssignAdd(dst, src string, c int) Instr {
	return Instr{Kind: OpAssignAdd, Dst: dst, Src: src, C: c}
}
func NewArray(arr string, n int) Instr    { return Instr{Kind: OpNewArray, Arr: arr, C: n} }
func AssignArray(dst, src string) Instr   { return Instr{Kind: OpAssignArray, Arr: dst, Src: src} }
func Check(arr string, idx Operand) Instr { return Instr{Kind: OpCheck, Arr: arr, Idx: idx} }

// TermKind classifies block terminators.
type TermKind int

const (
	TJump TermKind = iota
	TBranch
	TReturn
)

// Term is a block terminator. Branch condition is Left Op Right.
type Term struct {
	Kind        TermKind
	Target      string // Jump
	Left, Right Operand
	Op          string // < <= > >= == !=
	Then, Else  string
}

func Jump(target string) Term { return Term{Kind: TJump, Target: target} }
func Return() Term            { return Term{Kind: TReturn} }
func Branch(l Operand, op string, r Operand, then, els string) Term {
	return Term{Kind: TBranch, Left: l, Op: op, Right: r, Then: then, Else: els}
}

// Block is a basic block. Terms must contain exactly one terminator.
type Block struct {
	Name   string
	Instrs []Instr
	Terms  []Term
}

// Program is one complete IR input.
type Program struct {
	Name    string
	Vars    []string       // scalar variable declarations
	Inputs  []string       // vars whose values come from runtime input
	Arrays  []string       // array variable declarations
	Lengths map[string]int // constant length at declaration
	Entry   string
	Blocks  []*Block
}

// Block finds a block by name.
func (p *Program) Block(name string) *Block {
	for _, b := range p.Blocks {
		if b.Name == name {
			return b
		}
	}
	return nil
}
