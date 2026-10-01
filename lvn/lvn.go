// Package lvn implements a basic-block local value numbering pass.
//
// Instructions are appended in order; each value-producing instruction
// receives a value number (consecutive, starting at 1) and a reuse flag
// indicating whether an identical computation was already numbered.
package lvn

import (
	"errors"
	"fmt"
	"sync"
)

// Op identifies the instruction kind.
type Op int

const (
	OpConst Op = iota // CONST c: produce constant c
	OpLoad            // LOAD s: read slot s
	OpAdd             // ADD a b: commutative
	OpMul             // MUL a b: commutative
	OpSub             // SUB a b: not commutative
	OpStore           // STORE s v: store value number v into slot s
	OpCall            // CALL: barrier, always produces a fresh value
)

func (op Op) String() string {
	switch op {
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
	return fmt.Sprintf("Op(%d)", int(op))
}

// Instr is a single basic-block instruction. Use the constructors
// (Const, Load, Add, Mul, Sub, Store, Call) to build valid instructions.
type Instr struct {
	op Op
	c  int64 // CONST: constant value
	s  int64 // LOAD/STORE: slot index
	a  int   // ADD/MUL/SUB: left operand value number
	b  int   // ADD/MUL/SUB: right operand value number
	v  int   // STORE: stored value number
}

// Const builds a CONST c instruction.
func Const(c int64) Instr { return Instr{op: OpConst, c: c} }

// Load builds a LOAD s instruction reading slot s.
func Load(s int64) Instr { return Instr{op: OpLoad, s: s} }

// Add builds an ADD a b instruction (commutative).
func Add(a, b int) Instr { return Instr{op: OpAdd, a: a, b: b} }

// Mul builds a MUL a b instruction (commutative).
func Mul(a, b int) Instr { return Instr{op: OpMul, a: a, b: b} }

// Sub builds a SUB a b instruction (not commutative).
func Sub(a, b int) Instr { return Instr{op: OpSub, a: a, b: b} }

// Store builds a STORE s v instruction storing value number v into slot s.
func Store(s int64, v int) Instr { return Instr{op: OpStore, s: s, v: v} }

// Call builds a CALL instruction.
func Call() Instr { return Instr{op: OpCall} }

// Decompose returns the instruction kind and its canonical argument
// list: CONST [c]; LOAD [slot]; ADD/MUL/SUB [a, b]; STORE [slot, v];
// CALL [].
func (in Instr) Decompose() (Op, []int64) {
	switch in.op {
	case OpConst:
		return in.op, []int64{in.c}
	case OpLoad:
		return in.op, []int64{in.s}
	case OpAdd, OpMul, OpSub:
		return in.op, []int64{int64(in.a), int64(in.b)}
	case OpStore:
		return in.op, []int64{in.s, int64(in.v)}
	}
	return in.op, nil
}

func (in Instr) String() string {
	op, args := in.Decompose()
	switch in.op {
	case OpConst:
		return fmt.Sprintf("CONST %d", args[0])
	case OpLoad:
		return fmt.Sprintf("LOAD %d", args[0])
	case OpAdd, OpMul, OpSub:
		return fmt.Sprintf("%s %d %d", op, args[0], args[1])
	case OpStore:
		return fmt.Sprintf("STORE %d %d", args[0], args[1])
	}
	return "CALL"
}

// Rejection reasons returned by Append and Seal. Use errors.Is to
// distinguish them.
var (
	// ErrSealed: instruction appended after the block was sealed.
	ErrSealed = errors.New("lvn: block already sealed")
	// ErrOperand: an operand references a value number that does not exist.
	ErrOperand = errors.New("lvn: operand references unknown value number")
	// ErrSlot: a negative slot index was used.
	ErrSlot = errors.New("lvn: negative slot index")
	// ErrReseal: the block was sealed more than once.
	ErrReseal = errors.New("lvn: block sealed more than once")
)

// Result describes the outcome of a successfully appended instruction.
type Result struct {
	Value     int    // assigned value number; 0 for STORE (produces no value)
	Reused    bool   // true if an existing value number was reused
	Redundant bool   // STORE only: known value already equals v, version unchanged
	Reason    string // human-readable decision rationale (for logs)
}

// key is the value-numbering table key: (op, normalized operands).
// LOAD keys additionally carry the slot version and call epoch.
type key struct {
	op    Op
	x     int64 // CONST: constant; LOAD: slot; ADD/MUL/SUB: first (normalized) operand
	y     int64 // ADD/MUL/SUB: second (normalized) operand
	ver   uint64
	epoch uint64
}

// Block is a single basic block under value numbering. It is safe for
// concurrent use; the result of concurrent calls is equivalent to some
// serial order.
type Block struct {
	mu       sync.Mutex
	sealed   bool
	next     int              // next value number to assign
	table    map[key]int      // (op, normalized operands) -> value number
	versions map[int64]uint64 // slot -> store version
	known    map[int64]int    // slot -> known value number (set by STORE only)
	epoch    uint64           // call epoch
}

// NewBlock returns an empty block. Slot versions and the call epoch
// start at 0; the first assigned value number is 1.
func NewBlock() *Block {
	return &Block{
		next:     1,
		table:    make(map[key]int),
		versions: make(map[int64]uint64),
		known:    make(map[int64]int),
	}
}

// Append validates and applies one instruction, returning its value
// number and reuse information. A rejected instruction leaves the
// block state untouched. Validation order is: sealed, operand, slot.
func (b *Block) Append(in Instr) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sealed {
		return Result{}, fmt.Errorf("%w: cannot append %s", ErrSealed, in.op)
	}
	if err := b.checkOperands(in); err != nil {
		return Result{}, err
	}
	if err := checkSlot(in); err != nil {
		return Result{}, err
	}

	switch in.op {
	case OpConst:
		return b.lookupOrAssign(key{op: OpConst, x: in.c},
			fmt.Sprintf("CONST key=(CONST %d)", in.c)), nil
	case OpAdd, OpMul:
		lo, hi := in.a, in.b
		if lo > hi {
			lo, hi = hi, lo
		}
		return b.lookupOrAssign(key{op: in.op, x: int64(lo), y: int64(hi)},
			fmt.Sprintf("%s key=(%s %d %d) commutative-normalized", in.op, in.op, lo, hi)), nil
	case OpSub:
		return b.lookupOrAssign(key{op: OpSub, x: int64(in.a), y: int64(in.b)},
			fmt.Sprintf("SUB key=(SUB %d %d) operand order kept", in.a, in.b)), nil
	case OpLoad:
		if v, ok := b.known[in.s]; ok {
			return Result{Value: v, Reused: true,
				Reason: fmt.Sprintf("LOAD slot %d forwarded known value %d from STORE", in.s, v)}, nil
		}
		k := key{op: OpLoad, x: in.s, ver: b.versions[in.s], epoch: b.epoch}
		return b.lookupOrAssign(k,
			fmt.Sprintf("LOAD key=(LOAD slot=%d ver=%d epoch=%d)", in.s, k.ver, k.epoch)), nil
	case OpStore:
		if v, ok := b.known[in.s]; ok && v == in.v {
			return Result{Redundant: true,
				Reason: fmt.Sprintf("STORE slot %d already known to hold value %d, version unchanged", in.s, in.v)}, nil
		}
		b.versions[in.s]++
		b.known[in.s] = in.v
		return Result{
			Reason: fmt.Sprintf("STORE slot %d <- value %d, version bumped to %d", in.s, in.v, b.versions[in.s])}, nil
	case OpCall:
		b.epoch++
		clear(b.known)
		v := b.next
		b.next++
		return Result{Value: v,
			Reason: fmt.Sprintf("CALL always fresh, epoch bumped to %d, known values cleared", b.epoch)}, nil
	}
	return Result{}, fmt.Errorf("lvn: unknown op %d", int(in.op))
}

// checkOperands validates value-number operands (checked before slots).
func (b *Block) checkOperands(in Instr) error {
	valid := func(v int) bool { return v >= 1 && v < b.next }
	switch in.op {
	case OpAdd, OpMul, OpSub:
		if !valid(in.a) || !valid(in.b) {
			return fmt.Errorf("%w: %s operands (%d, %d), next value number is %d",
				ErrOperand, in.op, in.a, in.b, b.next)
		}
	case OpStore:
		if !valid(in.v) {
			return fmt.Errorf("%w: STORE value %d, next value number is %d",
				ErrOperand, in.v, b.next)
		}
	}
	return nil
}

// checkSlot validates the slot index (checked after operands).
func checkSlot(in Instr) error {
	if (in.op == OpLoad || in.op == OpStore) && in.s < 0 {
		return fmt.Errorf("%w: %s slot %d", ErrSlot, in.op, in.s)
	}
	return nil
}

// lookupOrAssign returns the existing value number for k, or assigns the
// next one. The caller must hold b.mu.
func (b *Block) lookupOrAssign(k key, why string) Result {
	if v, ok := b.table[k]; ok {
		return Result{Value: v, Reused: true, Reason: why + fmt.Sprintf(" -> hit, reuse value %d", v)}
	}
	v := b.next
	b.next++
	b.table[k] = v
	return Result{Value: v, Reason: why + fmt.Sprintf(" -> miss, assign value %d", v)}
}

// Seal closes the block to further appends. Sealing twice is rejected
// with ErrReseal.
func (b *Block) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrReseal
	}
	b.sealed = true
	return nil
}

// Snapshot is a consistent read-only view of the block state.
type Snapshot struct {
	Sealed   bool
	Next     int              // next value number to assign
	Epoch    uint64           // current call epoch
	Versions map[int64]uint64 // slot -> store version
	Known    map[int64]int    // slot -> known value number
	Entries  int              // number of table entries
}

// Snapshot returns a consistent copy of the block state.
func (b *Block) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := Snapshot{
		Sealed:   b.sealed,
		Next:     b.next,
		Epoch:    b.epoch,
		Versions: make(map[int64]uint64, len(b.versions)),
		Known:    make(map[int64]int, len(b.known)),
		Entries:  len(b.table),
	}
	for s, v := range b.versions {
		snap.Versions[s] = v
	}
	for s, v := range b.known {
		snap.Known[s] = v
	}
	return snap
}
