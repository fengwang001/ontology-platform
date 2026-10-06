// Package bce implements a bounds-check elimination subsystem: it decides,
// for every bounds check in a control-flow IR, whether the check can be
// safely removed using only four kinds of provable facts (constant index,
// constant array length, path comparison conditions, and previously passed
// checks), and reports a reason for every check that must be kept.
package bce

import "fmt"

// Opnd is an instruction operand: either a constant or a variable.
type Opnd struct {
	IsConst bool
	Const   int64
	Var     string
}

// C builds a constant operand.
func C(v int64) Opnd { return Opnd{IsConst: true, Const: v} }

// V builds a variable operand.
func V(name string) Opnd { return Opnd{Var: name} }

// BinOp is a scalar binary operator.
type BinOp int

const (
	Add BinOp = iota
	Sub
)

// CmpOp is a comparison operator used in branch conditions.
type CmpOp int

const (
	LT CmpOp = iota
	LE
	GT
	GE
	EQ
	NE
)

func (op CmpOp) String() string {
	switch op {
	case LT:
		return "<"
	case LE:
		return "<="
	case GT:
		return ">"
	case GE:
		return ">="
	case EQ:
		return "=="
	case NE:
		return "!="
	}
	return "?"
}

// Negate returns the operator that holds exactly when op does not.
func (op CmpOp) Negate() CmpOp {
	switch op {
	case LT:
		return GE
	case LE:
		return GT
	case GT:
		return LE
	case GE:
		return LT
	case EQ:
		return NE
	case NE:
		return EQ
	}
	return op
}

// Flip swaps the two sides of the comparison.
func (op CmpOp) Flip() CmpOp {
	switch op {
	case LT:
		return GT
	case LE:
		return GE
	case GT:
		return LT
	case GE:
		return LE
	}
	return op
}

// Instr is any instruction, including terminators.
type Instr interface{ isInstr() }

// Const defines Dst as a constant scalar.
type Const struct {
	Dst string
	Val int64
}

// Input defines Dst from the (unknown) program input.
type Input struct{ Dst string }

// Bin defines Dst = A op B.
type Bin struct {
	Dst string
	Op  BinOp
	A   Opnd
	B   Opnd
}

// Copy defines Dst = Src (scalar).
type Copy struct {
	Dst string
	Src string
}

// NewArray defines Dst as a fresh array of the given length.
type NewArray struct {
	Dst  string
	Size Opnd
}

// ArrCopy reassigns array variable Dst to the array held by Src.
type ArrCopy struct {
	Dst string
	Src string
}

// LenOf defines Dst = len(Arr).
type LenOf struct {
	Dst string
	Arr string
}

// Check is a subscript access Idx into Arr guarded by a bounds check.
// ID uniquely identifies the check in the report.
type Check struct {
	ID  string
	Arr string
	Idx string
}

// Jmp is an unconditional terminator.
type Jmp struct{ To string }

// Br is a conditional terminator: if A op B then Then else Else.
type Br struct {
	A    Opnd
	Op   CmpOp
	B    Opnd
	Then string
	Else string
}

// Ret ends the function.
type Ret struct{}

func (Const) isInstr()    {}
func (Input) isInstr()    {}
func (Bin) isInstr()      {}
func (Copy) isInstr()     {}
func (NewArray) isInstr() {}
func (ArrCopy) isInstr()  {}
func (LenOf) isInstr()    {}
func (Check) isInstr()    {}
func (Jmp) isInstr()      {}
func (Br) isInstr()       {}
func (Ret) isInstr()      {}

func isTerminator(i Instr) bool {
	switch i.(type) {
	case Jmp, Br, Ret:
		return true
	}
	return false
}

// Block is a straight-line instruction sequence ending in one terminator.
type Block struct {
	ID     string
	Instrs []Instr
}

// Term returns the block's terminator (the last instruction), or nil.
func (b *Block) Term() Instr {
	if len(b.Instrs) == 0 {
		return nil
	}
	t := b.Instrs[len(b.Instrs)-1]
	if isTerminator(t) {
		return t
	}
	return nil
}

// Program is a control-flow graph of basic blocks.
type Program struct {
	Entry  string
	Blocks []*Block
}

// ErrKind classifies invalid-input errors. The declaration order is the
// rejection order: undefined references are reported first, then multiple
// terminators, then unreachable blocks, then loops without a unique entry.
type ErrKind int

const (
	ErrUndefinedRef ErrKind = iota
	ErrMultiTerminator
	ErrUnreachableBlock
	ErrLoopMultiEntry
)

func (k ErrKind) String() string {
	switch k {
	case ErrUndefinedRef:
		return "undefined-reference"
	case ErrMultiTerminator:
		return "multiple-terminators"
	case ErrUnreachableBlock:
		return "unreachable-block"
	case ErrLoopMultiEntry:
		return "loop-multiple-entry"
	}
	return "unknown"
}

// Error is an invalid-input rejection. No partial result is produced.
type Error struct {
	Kind   ErrKind
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Detail) }

// cfg is the registered control-flow view of a valid program.
type cfg struct {
	prog   *Program
	index  map[string]int // block ID -> position in prog.Blocks
	preds  [][]int
	succs  [][]int
	checks []checkSite
}

// checkSite locates one Check instruction.
type checkSite struct {
	chk   *Check
	block int
	pos   int // instruction index inside the block
}

// validate checks the input and, when valid, registers the control flow.
// Errors are reported in the fixed rejection order of ErrKind.
func validate(p *Program) (*cfg, error) {
	if p == nil || len(p.Blocks) == 0 {
		return nil, &Error{ErrUndefinedRef, "program has no blocks"}
	}
	index := map[string]int{}
	for _, b := range p.Blocks {
		if _, dup := index[b.ID]; dup {
			return nil, &Error{ErrUndefinedRef, "duplicate block " + b.ID}
		}
		index[b.ID] = len(index)
	}
	if _, ok := index[p.Entry]; !ok {
		return nil, &Error{ErrUndefinedRef, "entry block " + p.Entry + " not defined"}
	}

	// 1. Undefined references: every used variable must be defined
	// somewhere, array operands must be arrays, branch targets must exist.
	scalarDefs := map[string]bool{}
	arrayDefs := map[string]bool{}
	for _, b := range p.Blocks {
		for _, ins := range b.Instrs {
			switch t := ins.(type) {
			case Const:
				scalarDefs[t.Dst] = true
			case Input:
				scalarDefs[t.Dst] = true
			case Bin:
				scalarDefs[t.Dst] = true
			case Copy:
				scalarDefs[t.Dst] = true
			case LenOf:
				scalarDefs[t.Dst] = true
			case NewArray:
				arrayDefs[t.Dst] = true
			case ArrCopy:
				arrayDefs[t.Dst] = true
			}
		}
	}
	useVar := func(name string) error {
		if !scalarDefs[name] {
			return &Error{ErrUndefinedRef, "undefined scalar variable " + name}
		}
		return nil
	}
	useOpnd := func(o Opnd) error {
		if o.IsConst {
			return nil
		}
		return useVar(o.Var)
	}
	for _, b := range p.Blocks {
		for _, ins := range b.Instrs {
			switch t := ins.(type) {
			case Bin:
				if err := useOpnd(t.A); err != nil {
					return nil, err
				}
				if err := useOpnd(t.B); err != nil {
					return nil, err
				}
			case Copy:
				if err := useVar(t.Src); err != nil {
					return nil, err
				}
			case NewArray:
				if err := useOpnd(t.Size); err != nil {
					return nil, err
				}
			case ArrCopy:
				if !arrayDefs[t.Src] {
					return nil, &Error{ErrUndefinedRef, "undefined array " + t.Src}
				}
			case LenOf:
				if !arrayDefs[t.Arr] {
					return nil, &Error{ErrUndefinedRef, "undefined array " + t.Arr}
				}
			case Check:
				if !arrayDefs[t.Arr] {
					return nil, &Error{ErrUndefinedRef, "undefined array " + t.Arr}
				}
				if err := useVar(t.Idx); err != nil {
					return nil, err
				}
			case Jmp:
				if _, ok := index[t.To]; !ok {
					return nil, &Error{ErrUndefinedRef, "undefined block " + t.To}
				}
			case Br:
				if err := useOpnd(t.A); err != nil {
					return nil, err
				}
				if err := useOpnd(t.B); err != nil {
					return nil, err
				}
				if _, ok := index[t.Then]; !ok {
					return nil, &Error{ErrUndefinedRef, "undefined block " + t.Then}
				}
				if _, ok := index[t.Else]; !ok {
					return nil, &Error{ErrUndefinedRef, "undefined block " + t.Else}
				}
			}
		}
	}

	// 2. Exactly one terminator per block, in last position.
	for _, b := range p.Blocks {
		n := 0
		for pos, ins := range b.Instrs {
			if isTerminator(ins) {
				n++
				if pos != len(b.Instrs)-1 {
					return nil, &Error{ErrMultiTerminator,
						fmt.Sprintf("block %s: terminator not in last position", b.ID)}
				}
			}
		}
		if n != 1 {
			return nil, &Error{ErrMultiTerminator,
				fmt.Sprintf("block %s: %d terminators", b.ID, n)}
		}
	}

	// Register control flow.
	c := &cfg{prog: p, index: index}
	c.preds = make([][]int, len(p.Blocks))
	c.succs = make([][]int, len(p.Blocks))
	addEdge := func(from, to string) {
		f, t := index[from], index[to]
		c.succs[f] = append(c.succs[f], t)
		c.preds[t] = append(c.preds[t], f)
	}
	for _, b := range p.Blocks {
		switch t := b.Term().(type) {
		case Jmp:
			addEdge(b.ID, t.To)
		case Br:
			addEdge(b.ID, t.Then)
			if t.Else != t.Then {
				addEdge(b.ID, t.Else)
			}
		}
	}
	for i, b := range p.Blocks {
		for pos, ins := range b.Instrs {
			if chk, ok := ins.(Check); ok {
				chk := chk
				c.checks = append(c.checks, checkSite{chk: &chk, block: i, pos: pos})
			}
		}
	}

	// 3. Every block must be reachable from the entry.
	entry := index[p.Entry]
	seen := make([]bool, len(p.Blocks))
	queue := []int{entry}
	seen[entry] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, s := range c.succs[cur] {
			if !seen[s] {
				seen[s] = true
				queue = append(queue, s)
			}
		}
	}
	for i, b := range p.Blocks {
		if !seen[i] {
			return nil, &Error{ErrUnreachableBlock, "block " + b.ID + " is not reachable from entry"}
		}
	}

	// 4. Every loop must have a unique entry. A loop is a strongly
	// connected component with a cycle; its entries are the blocks with a
	// predecessor outside the component (plus the entry block itself).
	n := len(p.Blocks)
	reach := make([][]bool, n)
	for i := 0; i < n; i++ {
		reach[i] = make([]bool, n)
		queue := []int{i}
		reach[i][i] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, s := range c.succs[cur] {
				if !reach[i][s] {
					reach[i][s] = true
					queue = append(queue, s)
				}
			}
		}
	}
	done := make([]bool, n)
	for b := 0; b < n; b++ {
		if done[b] {
			continue
		}
		var scc []int
		inSCC := make([]bool, n)
		for m := 0; m < n; m++ {
			if reach[b][m] && reach[m][b] {
				scc = append(scc, m)
				inSCC[m] = true
			}
		}
		cyclic := len(scc) > 1
		if !cyclic { // single block: cyclic only via a self-loop
			for _, s := range c.succs[b] {
				if s == b {
					cyclic = true
				}
			}
		}
		for _, m := range scc {
			done[m] = true
		}
		if !cyclic {
			continue
		}
		entries := map[int]bool{}
		for _, m := range scc {
			if m == entry {
				entries[m] = true
			}
			for _, pr := range c.preds[m] {
				if !inSCC[pr] {
					entries[m] = true
				}
			}
		}
		if len(entries) > 1 {
			return nil, &Error{ErrLoopMultiEntry,
				fmt.Sprintf("loop containing block %s has %d entries", p.Blocks[b].ID, len(entries))}
		}
	}
	return c, nil
}
