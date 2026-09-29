package vm

import "fmt"

// Opcode is a single bytecode operation understood by the interpreter.
type Opcode int

// Instruction set of the stack machine.
const (
	OpPush   Opcode = iota // operand: value to push
	OpAdd                  // pop a, pop b; push b+a
	OpLoad                 // pop key; push storage[key] (0 if absent)
	OpStore                // pop key, pop value; storage[key]=value
	OpExpand               // operand: expand memory to at least operand words
	OpCall                 // A=target index, B=requested fuel
	OpReturn
	OpFail
)

// Instruction is one decoded bytecode step.
type Instruction struct {
	Op      Opcode
	Operand int64 // PUSH value / EXPAND target words
	A, B    int64 // CALL: A=target, B=requested fuel
}

// Program is a named routine: a flat instruction list.
type Program struct {
	Name string
	Code []Instruction
}

// FailReason distinguishes frame failure causes.
type FailReason int

const (
	FailNone FailReason = iota
	FailOutOfFuel
	FailStackUnderflow
	FailExplicit
	FailDepthExceeded
)

func (r FailReason) String() string {
	switch r {
	case FailNone:
		return "none"
	case FailOutOfFuel:
		return "out-of-fuel"
	case FailStackUnderflow:
		return "stack-underflow"
	case FailExplicit:
		return "explicit-fail"
	case FailDepthExceeded:
		return "depth-exceeded"
	default:
		return "unknown"
	}
}

// StaticError describes why a whole program set is rejected before execution.
type StaticError struct{ Msg string }

func (e *StaticError) Error() string { return "static error: " + e.Msg }

// Programs is an indexed routine table; call targets are indices into it.
type Programs []Program

// Validate performs all pre-execution static checks. The whole program set is
// rejected (and nothing executes) if any routine contains an unknown opcode,
// a CALL to a non-existent target / with negative fuel, or a negative EXPAND.
func (ps Programs) Validate() error {
	for i := range ps {
		p := &ps[i]
		for pc, ins := range p.Code {
			switch ins.Op {
			case OpPush, OpAdd, OpLoad, OpStore, OpReturn, OpFail:
				// no operands to check (Push/Expand may carry any value)
			case OpExpand:
				if ins.Operand < 0 {
					return &StaticError{Msg: fmt.Sprintf("program %d (%s) pc %d: negative memory expansion %d", i, p.Name, pc, ins.Operand)}
				}
			case OpCall:
				if ins.A < 0 || int(ins.A) >= len(ps) {
					return &StaticError{Msg: fmt.Sprintf("program %d (%s) pc %d: call target %d does not exist", i, p.Name, pc, ins.A)}
				}
				if ins.B < 0 {
					return &StaticError{Msg: fmt.Sprintf("program %d (%s) pc %d: negative call fuel %d", i, p.Name, pc, ins.B)}
				}
			default:
				return &StaticError{Msg: fmt.Sprintf("program %d (%s) pc %d: unknown opcode %d", i, p.Name, pc, ins.Op)}
			}
		}
	}
	return nil
}
