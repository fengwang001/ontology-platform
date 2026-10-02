package ontology

import (
	"fmt"
	"strconv"
	"sync"
)

// Op enumerates the local value numbering instruction kinds.
type Op int

const (
	OpConst Op = iota
	OpLoad
	OpAdd
	OpMul
	OpSub
	OpStore
	OpCall
)

// Ins is one instruction in program order.
//
// Field usage by kind:
//   - CONST: Val holds the constant c.
//   - LOAD:  Slot holds s.
//   - ADD/MUL/SUB: A and B hold the operand value numbers.
//   - STORE: Slot holds s, V holds the stored value number.
//   - CALL:  no fields.
type Ins struct {
	Kind Op
	Val  int64
	Slot int
	A    int
	B    int
	V    int
}

// Result reports the outcome of appending one instruction.
type Result struct {
	// VN is the value number produced by the instruction (>=1); it is 0 for
	// STORE, which produces no value.
	VN int
	// Reused is true when an existing value number was returned instead of a
	// fresh one (including forwarded LOADs). For STORE it means the store was
	// redundant and did not bump the slot version.
	Reused bool
	// Key is the human-readable table key used for the reuse decision.
	Key string
	// Reason explains the decision (fresh allocation, reuse, forwarding, ...).
	Reason string
}

// Instruction constructors, for readable call sites and tests.

// Const returns a CONST c instruction.
func Const(c int64) Ins { return Ins{Kind: OpConst, Val: c} }

// Load returns a LOAD s instruction.
func Load(s int) Ins { return Ins{Kind: OpLoad, Slot: s} }

// Add returns an ADD a b instruction (commutative).
func Add(a, b int) Ins { return Ins{Kind: OpAdd, A: a, B: b} }

// Mul returns a MUL a b instruction (commutative).
func Mul(a, b int) Ins { return Ins{Kind: OpMul, A: a, B: b} }

// Sub returns a SUB a b instruction (non-commutative).
func Sub(a, b int) Ins { return Ins{Kind: OpSub, A: a, B: b} }

// Store returns a STORE s v instruction.
func Store(s, v int) Ins { return Ins{Kind: OpStore, Slot: s, V: v} }

// Call returns a CALL instruction.
func Call() Ins { return Ins{Kind: OpCall} }

// String renders the instruction in the syntax of the task description.
func (ins Ins) String() string {
	switch ins.Kind {
	case OpConst:
		return "CONST " + strconv.FormatInt(ins.Val, 10)
	case OpLoad:
		return "LOAD " + strconv.Itoa(ins.Slot)
	case OpAdd:
		return fmt.Sprintf("ADD %d %d", ins.A, ins.B)
	case OpMul:
		return fmt.Sprintf("MUL %d %d", ins.A, ins.B)
	case OpSub:
		return fmt.Sprintf("SUB %d %d", ins.A, ins.B)
	case OpStore:
		return fmt.Sprintf("STORE %d %d", ins.Slot, ins.V)
	case OpCall:
		return "CALL"
	default:
		return "UNKNOWN"
	}
}

// BlockError carries a machine-distinguishable rejection reason.
type BlockError struct {
	Reason string
}

func (e *BlockError) Error() string { return "lvn: " + e.Reason }

// Predefined, distinguishable rejection reasons.
var (
	ErrSealed   = &BlockError{Reason: "block already sealed"}
	ErrBadValue = &BlockError{Reason: "operand references nonexistent value number"}
	ErrBadSlot  = &BlockError{Reason: "negative slot number"}
)

// exprKey is the canonical map key for pure computations (CONST/ADD/MUL/SUB)
// and for LOADs that are not forwarded from a known slot value.
//
// Pure keys never carry the call epoch, so they survive CALLs. LOAD keys
// carry the slot, its store-version and the call epoch: any of them changing
// produces a new value number.
type exprKey struct {
	kind  Op
	val   int64 // CONST value (only meaningful for OpConst)
	slot  int   // LOAD slot (only meaningful for OpLoad)
	x, y  int   // normalized operands (ADD/MUL/SUB)
	ver   int   // LOAD slot store-version
	epoch int   // LOAD call epoch
}

// Block is a concurrent-safe basic block local value numberer.
//
// The zero value is ready to use.
type Block struct {
	mu sync.Mutex

	sealed bool

	// nextVN is the next value number to allocate (1-based, dense).
	nextVN int

	// table maps canonical keys to existing value numbers. Pure keys
	// (CONST/ADD/MUL/SUB) persist across CALLs; LOAD keys embed epoch/version
	// and therefore become distinct after a CALL or a non-redundant STORE.
	table map[exprKey]int

	// versions[s] is the number of non-redundant STOREs to slot s so far.
	versions map[int]int

	// known[s] is the value number stored by the most recent STORE s, provided
	// no CALL happened afterwards; absent otherwise. LOAD never populates it.
	known map[int]int

	// epoch counts CALLs; every CALL clears all known values.
	epoch int
}

// NewBlock returns an empty basic block.
func NewBlock() *Block {
	return &Block{
		nextVN:   1,
		table:    make(map[exprKey]int),
		versions: make(map[int]int),
		known:    make(map[int]int),
	}
}

// Append validates and appends one instruction, returning its value number
// decision.
//
// Rejection order is fixed: sealed block first, then nonexistent operand
// value numbers, then negative slot numbers. A rejected instruction changes
// no state and allocates no value number.
func (b *Block) Append(ins Ins) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sealed {
		return Result{}, ErrSealed
	}

	// Validate operand value numbers before slot numbers: a rejected
	// instruction must not disturb any state.
	switch ins.Kind {
	case OpAdd, OpMul, OpSub:
		if !b.knownVN(ins.A) || !b.knownVN(ins.B) {
			return Result{}, ErrBadValue
		}
	case OpStore:
		if !b.knownVN(ins.V) {
			return Result{}, ErrBadValue
		}
	}

	switch ins.Kind {
	case OpLoad, OpStore:
		if ins.Slot < 0 {
			return Result{}, ErrBadSlot
		}
	}

	switch ins.Kind {
	case OpConst:
		return b.appendConst(ins), nil
	case OpLoad:
		return b.appendLoad(ins), nil
	case OpAdd, OpMul, OpSub:
		return b.appendBinop(ins), nil
	case OpStore:
		return b.appendStore(ins), nil
	case OpCall:
		return b.appendCall(), nil
	default:
		return Result{}, &BlockError{Reason: "unknown instruction kind"}
	}
}

// Seal closes the block; appending afterwards fails with ErrSealed. Sealing an
// already sealed block fails too.
func (b *Block) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	b.sealed = true
	return nil
}

func (b *Block) knownVN(v int) bool { return v >= 1 && v < b.nextVN }

func (b *Block) alloc() int {
	v := b.nextVN
	b.nextVN++
	return v
}

func (b *Block) appendConst(ins Ins) Result {
	key := exprKey{kind: OpConst, val: ins.Val}
	if vn, ok := b.table[key]; ok {
		return Result{
			VN:     vn,
			Reused: true,
			Key:    "CONST " + strconv.FormatInt(ins.Val, 10),
			Reason: "identical CONST key already present; pure keys ignore call epoch",
		}
	}
	vn := b.alloc()
	b.table[key] = vn
	return Result{
		VN:     vn,
		Key:    "CONST " + strconv.FormatInt(ins.Val, 10),
		Reason: "fresh value number for new CONST key",
	}
}

func (b *Block) appendBinop(ins Ins) Result {
	x, y := ins.A, ins.B
	if ins.Kind == OpAdd || ins.Kind == OpMul {
		// Commutative: normalize operands by ascending value number.
		if x > y {
			x, y = y, x
		}
	}
	key := exprKey{kind: ins.Kind, x: x, y: y}
	keyText := binopName(ins.Kind) + fmt.Sprintf("(%d,%d)", x, y)
	if vn, ok := b.table[key]; ok {
		why := "identical normalized operands"
		if ins.A != x || ins.B != y {
			why = "commutative operand order normalized to ascending value numbers"
		}
		if ins.Kind == OpSub {
			why = "identical operand order (SUB is non-commutative)"
		}
		return Result{
			VN:     vn,
			Reused: true,
			Key:    keyText,
			Reason: "key already present: " + why,
		}
	}
	vn := b.alloc()
	b.table[key] = vn
	why := "fresh value number for new expression key"
	if ins.Kind == OpAdd || ins.Kind == OpMul {
		why = "fresh value number after commutative normalization"
	}
	return Result{VN: vn, Key: keyText, Reason: why}
}

func (b *Block) appendLoad(ins Ins) Result {
	// Forward from the most recent STORE when no CALL invalidated it. A LOAD
	// result never becomes a known value itself.
	if vn, ok := b.known[ins.Slot]; ok {
		return Result{
			VN:     vn,
			Reused: true,
			Key:    fmt.Sprintf("SLOT %d KNOWN->%d", ins.Slot, vn),
			Reason: "load forwarding: slot holds value number from latest STORE and no CALL intervened",
		}
	}
	ver := b.versions[ins.Slot]
	key := exprKey{kind: OpLoad, slot: ins.Slot, ver: ver, epoch: b.epoch}
	keyText := fmt.Sprintf("LOAD(slot=%d,ver=%d,epoch=%d)", ins.Slot, ver, b.epoch)
	if vn, ok := b.table[key]; ok {
		return Result{
			VN:     vn,
			Reused: true,
			Key:    keyText,
			Reason: "same slot/version/epoch key already computed",
		}
	}
	vn := b.alloc()
	b.table[key] = vn
	return Result{
		VN:     vn,
		Key:    keyText,
		Reason: "fresh value number for slot/version/epoch load key",
	}
}

func (b *Block) appendStore(ins Ins) Result {
	// Redundant when the slot's known value is exactly v: keep the version
	// untouched. Note this also implies no CALL intervened (known is absent
	// after a CALL), so "known value equals v" is the exact criterion.
	if cur, ok := b.known[ins.Slot]; ok && cur == ins.V {
		return Result{
			Reused: true,
			Key:    fmt.Sprintf("STORE(slot=%d,v=%d)", ins.Slot, ins.V),
			Reason: "redundant store: slot already holds exactly this value; version unchanged",
		}
	}
	b.versions[ins.Slot]++
	b.known[ins.Slot] = ins.V
	return Result{
		Key:    fmt.Sprintf("STORE(slot=%d,v=%d)", ins.Slot, ins.V),
		Reason: "new stored value: slot version bumped and known value updated",
	}
}

func (b *Block) appendCall() Result {
	b.epoch++
	b.known = make(map[int]int)
	vn := b.alloc()
	return Result{
		VN:     vn,
		Key:    fmt.Sprintf("CALL#%d", b.epoch),
		Reason: "CALL is a barrier: always allocates a fresh value number, bumps epoch and clears known slot values",
	}
}

func binopName(k Op) string {
	switch k {
	case OpAdd:
		return "ADD"
	case OpMul:
		return "MUL"
	case OpSub:
		return "SUB"
	default:
		return "?"
	}
}

// Snapshot is a read-only point-in-time view of the block.
type Snapshot struct {
	Sealed   bool
	NextVN   int
	Epoch    int
	Versions map[int]int
	Known    map[int]int
	Table    map[string]int
}

// Query returns a consistent snapshot of the block state.
func (b *Block) Query() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	snap := Snapshot{
		Sealed:   b.sealed,
		NextVN:   b.nextVN,
		Epoch:    b.epoch,
		Versions: make(map[int]int, len(b.versions)),
		Known:    make(map[int]int, len(b.known)),
		Table:    make(map[string]int, len(b.table)),
	}
	for s, v := range b.versions {
		snap.Versions[s] = v
	}
	for s, v := range b.known {
		snap.Known[s] = v
	}
	for k, vn := range b.table {
		snap.Table[keyString(k)] = vn
	}
	return snap
}

func keyString(k exprKey) string {
	switch k.kind {
	case OpConst:
		return "CONST " + strconv.FormatInt(k.val, 10)
	case OpLoad:
		return fmt.Sprintf("LOAD(slot=%d,ver=%d,epoch=%d)", k.slot, k.ver, k.epoch)
	case OpAdd, OpMul, OpSub:
		return binopName(k.kind) + fmt.Sprintf("(%d,%d)", k.x, k.y)
	default:
		return "?"
	}
}
