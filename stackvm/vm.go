// Package stackvm implements a fuel-metered stack instruction interpreter
// with nested calls, monotonic word-addressed memory expansion, storage
// transactions that roll back on frame failure, and a serializing shared
// storage so concurrent programs execute atomically.
package stackvm

// Op is the opcode of an instruction.
type Op uint8

const (
	OpInvalid Op = iota
	OpPush
	OpAdd
	OpSLoad
	OpSStore
	OpMExpand
	OpCall
	OpReturn
	OpFail
)

// Instruction is one machine instruction. A and B are integer operands whose
// meaning depends on Op; TargetName names the callee for OpCall.
type Instruction struct {
	Op         Op
	A          int64
	B          int64
	TargetName string
}

// Program is a named list of instructions.
type Program struct {
	Name string
	Code []Instruction
}

// Target returns the call target program name.
func (ins Instruction) Target() string {
	return ins.TargetName
}

// FailCause identifies the runtime reason a frame failed.
type FailCause uint8

const (
	CauseNone FailCause = iota
	CauseFuelExhausted
	CauseStackUnderflow
	CauseExplicitFail
	CauseDepthExceeded
)

// StaticError describes a program rejection discovered before execution.
// The whole batch is rejected and nothing is executed.
type StaticError struct {
	Program string
	Reason  string
	PC      int
}

// FrameAccounting is the per-frame fuel ledger.
type FrameAccounting struct {
	Program  string
	Depth    int
	Initial  uint64
	Spent    uint64
	Returned uint64
	Cause    FailCause
}

// Receipt is the result of one top-level execution.
type Receipt struct {
	Program     string
	Success     bool
	Cause       FailCause
	Output      []int64
	Frames      []FrameAccounting
	InitialFuel uint64
	SpentFuel   uint64
}

// Machine validates and runs programs against a shared store.
type Machine struct {
	store    *Storage
	maxDepth int

	programs map[string]*Program
}

// NewMachine creates an execution machine.
func NewMachine(store *Storage, maxDepth int) *Machine {
	return &Machine{
		store:    store,
		maxDepth: maxDepth,
		programs: make(map[string]*Program),
	}
}
