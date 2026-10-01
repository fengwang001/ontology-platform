// Package lvn implements a basic-block local value numbering pass.
//
// Instructions are appended in order; each value-producing instruction
// receives a contiguous value number (starting at 1) plus a reuse flag
// indicating whether the computation was already available. The table
// key is (op, normalized operands): commutative ops sort their operands
// by value number, CONST keys on its literal, and LOAD keys on
// (slot, slot store-version, call epoch) unless the slot has a known
// forwarded value.
package lvn

import (
	"errors"
	"fmt"
	"sync"
)

// Op identifies the instruction kind.
type Op int

const (
	OpConst Op = iota // CONST c: produce the constant c
	OpLoad            // LOAD s: read slot s
	OpAdd             // ADD a b: commutative
	OpMul             // MUL a b: commutative
	OpSub             // SUB a b: non-commutative
	OpStore           // STORE s v: store value number v into slot s
	OpCall            // CALL: barrier, produces a fresh value
)

func (o Op) String() string {
	switch o {
	case OpConst:
		return "CONST"
	case OpLoad:
		return "LOAD"
	case OpAdd:
		return "ADD"
	case OpMul:
		return "MUL"
	case OpSub:
		return "SUB"
	case OpStore:
		return "STORE"
	case OpCall:
		return "CALL"
	}
	return fmt.Sprintf("Op(%d)", int(o))
}

// Instr is a single basic-block instruction. Unused fields stay zero.
type Instr struct {
	Op    Op
	Const int64 // OpConst: literal value
	Slot  int   // OpLoad/OpStore: slot index
	A     int   // OpAdd/OpMul/OpSub: left operand value number
	B     int   // OpAdd/OpMul/OpSub: right operand value number
	V     int   // OpStore: stored value number
}

func Const(c int64) Instr  { return Instr{Op: OpConst, Const: c} }
func Load(s int) Instr     { return Instr{Op: OpLoad, Slot: s} }
func Add(a, b int) Instr   { return Instr{Op: OpAdd, A: a, B: b} }
func Mul(a, b int) Instr   { return Instr{Op: OpMul, A: a, B: b} }
func Sub(a, b int) Instr   { return Instr{Op: OpSub, A: a, B: b} }
func Store(s, v int) Instr { return Instr{Op: OpStore, Slot: s, V: v} }
func Call() Instr          { return Instr{Op: OpCall} }

func (ins Instr) String() string {
	switch ins.Op {
	case OpConst:
		return fmt.Sprintf("CONST %d", ins.Const)
	case OpLoad:
		return fmt.Sprintf("LOAD s%d", ins.Slot)
	case OpAdd:
		return fmt.Sprintf("ADD v%d v%d", ins.A, ins.B)
	case OpMul:
		return fmt.Sprintf("MUL v%d v%d", ins.A, ins.B)
	case OpSub:
		return fmt.Sprintf("SUB v%d v%d", ins.A, ins.B)
	case OpStore:
		return fmt.Sprintf("STORE s%d v%d", ins.Slot, ins.V)
	case OpCall:
		return "CALL"
	}
	return fmt.Sprintf("Instr{%d}", int(ins.Op))
}

// Result is the outcome of appending one instruction.
type Result struct {
	Produces  bool // the instruction yields a value number
	Value     int  // assigned or reused value number (valid iff Produces)
	Reused    bool // an existing value number was reused, none allocated
	Redundant bool // OpStore only: slot already held this exact value
}

func (r Result) String() string {
	if !r.Produces {
		if r.Redundant {
			return "redundant-store"
		}
		return "no-value"
	}
	if r.Reused {
		return fmt.Sprintf("v%d reused", r.Value)
	}
	return fmt.Sprintf("v%d new", r.Value)
}

// Rejection reasons, distinguishable via errors.Is.
var (
	ErrSealed     = errors.New("lvn: block is sealed")
	ErrBadOperand = errors.New("lvn: operand references a non-existent value number")
	ErrBadSlot    = errors.New("lvn: negative slot")
)

// tableKey is the value-numbering table key: (op, normalized operands).
// CONST uses konst; LOAD uses (slot, version, epoch); arithmetic uses a, b
// (sorted ascending for commutative ops). CALL is never tabled.
type tableKey struct {
	op      Op
	a, b    int
	konst   int64
	slot    int
	version int
	epoch   int
}

// Block is a single basic block under value numbering. It is safe for
// concurrent use; every operation behaves as some serial interleaving.
type Block struct {
	mu       sync.Mutex
	sealed   bool
	next     int // next value number to allocate, starts at 1
	table    map[tableKey]int
	known    map[int]int // slot -> forwarded value number (STORE only)
	versions map[int]int // slot -> store version
	epoch    int         // call epoch
}

// NewBlock returns an empty block; slot versions and the call epoch
// start at 0.
func NewBlock() *Block {
	return &Block{
		next:     1,
		table:    make(map[tableKey]int),
		known:    make(map[int]int),
		versions: make(map[int]int),
	}
}

// Append validates and applies one instruction. Rejected instructions
// leave the table, known values, versions and epoch untouched.
// Validation order: sealed, then operands, then slot.
func (b *Block) Append(ins Instr) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sealed {
		return Result{}, ErrSealed
	}

	switch ins.Op {
	case OpConst:
		return b.valueOf(tableKey{op: OpConst, konst: ins.Const}), nil

	case OpAdd, OpMul:
		if err := b.checkOperands(ins.A, ins.B); err != nil {
			return Result{}, err
		}
		a, c := ins.A, ins.B
		if a > c {
			a, c = c, a
		}
		return b.valueOf(tableKey{op: ins.Op, a: a, b: c}), nil

	case OpSub:
		if err := b.checkOperands(ins.A, ins.B); err != nil {
			return Result{}, err
		}
		return b.valueOf(tableKey{op: OpSub, a: ins.A, b: ins.B}), nil

	case OpLoad:
		if ins.Slot < 0 {
			return Result{}, fmt.Errorf("%w: s%d", ErrBadSlot, ins.Slot)
		}
		if v, ok := b.known[ins.Slot]; ok {
			return Result{Produces: true, Value: v, Reused: true}, nil
		}
		return b.valueOf(tableKey{
			op:      OpLoad,
			slot:    ins.Slot,
			version: b.versions[ins.Slot],
			epoch:   b.epoch,
		}), nil

	case OpStore:
		if err := b.checkOperands(ins.V); err != nil {
			return Result{}, err
		}
		if ins.Slot < 0 {
			return Result{}, fmt.Errorf("%w: s%d", ErrBadSlot, ins.Slot)
		}
		if v, ok := b.known[ins.Slot]; ok && v == ins.V {
			return Result{Redundant: true}, nil
		}
		b.versions[ins.Slot]++
		b.known[ins.Slot] = ins.V
		return Result{}, nil

	case OpCall:
		b.epoch++
		clear(b.known)
		vn := b.next
		b.next++
		return Result{Produces: true, Value: vn}, nil
	}
	return Result{}, fmt.Errorf("lvn: unknown op %d", int(ins.Op))
}

// valueOf looks up k, allocating the next value number on a miss.
func (b *Block) valueOf(k tableKey) Result {
	if vn, ok := b.table[k]; ok {
		return Result{Produces: true, Value: vn, Reused: true}
	}
	vn := b.next
	b.next++
	b.table[k] = vn
	return Result{Produces: true, Value: vn}
}

// checkOperands requires every operand to name an existing value number.
func (b *Block) checkOperands(ops ...int) error {
	for _, o := range ops {
		if o < 1 || o >= b.next {
			return fmt.Errorf("%w: v%d", ErrBadOperand, o)
		}
	}
	return nil
}

// Seal closes the block; sealing twice is rejected.
func (b *Block) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	b.sealed = true
	return nil
}

// Sealed reports whether the block has been sealed.
func (b *Block) Sealed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sealed
}

// ValueCount returns how many value numbers have been allocated so far;
// the numbers in use are exactly 1..ValueCount with no holes.
func (b *Block) ValueCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.next - 1
}

// CallEpoch returns the current call epoch.
func (b *Block) CallEpoch() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.epoch
}

// SlotVersion returns the current store version of slot s.
func (b *Block) SlotVersion(s int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.versions[s]
}
