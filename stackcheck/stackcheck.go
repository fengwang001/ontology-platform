// Package stackcheck 实现字节码函数的栈深静态校验器。
//
// 用 FIFO 工作表推导每条可达指令的入口栈深，确保所有控制流路径汇合栈深一致、
// 从不下溢、执行后栈深不超过上限且 RET 入口恰为 1。指令栈效应、推导顺序与错误
// 优先级详见同目录 README.md。入口 API 为 New / Validator.Register /
// Validator.MaxStack；Analyze 暴露与注册表无关的纯推演，返回入口栈深表
// （不可达为 -1）与最大栈深。
package stackcheck

import (
	"fmt"
	"sync"
)

// OpCode 是栈机指令操作码。
type OpCode int

const (
	PUSH OpCode = iota + 1
	POP
	ADD
	DUP
	JMP
	JZ
	RET
)

func (c OpCode) String() string {
	switch c {
	case PUSH:
		return "PUSH"
	case POP:
		return "POP"
	case ADD:
		return "ADD"
	case DUP:
		return "DUP"
	case JMP:
		return "JMP"
	case JZ:
		return "JZ"
	case RET:
		return "RET"
	default:
		return fmt.Sprintf("OP?%d", int(c))
	}
}

// Op 是一条指令。仅 JMP 与 JZ 使用 Target。
type Op struct {
	Code   OpCode
	Target int
}

// Reason 标识注册 / 查询被拒绝的可区分原因。
type Reason string

const (
	ReasonInvalidLimit      Reason = "invalid_limit"
	ReasonNameExists        Reason = "name_exists"
	ReasonEmptyProgram      Reason = "empty_program"
	ReasonUnknownOp         Reason = "unknown_op"
	ReasonJumpOutOfBounds   Reason = "jump_out_of_bounds"
	ReasonUnderflow         Reason = "underflow"
	ReasonOverflow          Reason = "overflow"
	ReasonMergeMismatch     Reason = "merge_mismatch"
	ReasonBadReturnDepth    Reason = "bad_return_depth"
	ReasonFallthroughBounds Reason = "fallthrough_out_of_bounds"
	ReasonNotFound          Reason = "not_found"
)

// Error 携带可机读的拒绝原因与定位信息。
type Error struct {
	Reason Reason
	Msg    string
	PC     int // 触发错误的指令下标；不适用时为 -1
	Depth  int // 触发错误时的入口栈深；不适用时为 -1
}

func (e *Error) Error() string {
	return string(e.Reason) + ": " + e.Msg
}

func newError(reason Reason, pc, depth int, msg string) *Error {
	return &Error{Reason: reason, PC: pc, Depth: depth, Msg: msg}
}

// Analyze 对指令序列做栈深数据流推导。
//
// 静态检查顺序：空序列 → 未知操作码（下标升序）→ 跳转目标越界（下标升序），
// 任一命中立即返回。静态检查通过后再做 FIFO 工作表数据流推导。
//
// 推导初始只将下标 0 以入口栈深 0 入队。取出指令后按「先下溢、后超限、
// 再 RET 栈深」检查本指令，随后按指令语义给后继（JZ 先目标后落空）赋
// 入口栈深并入队；后继已有不同入口栈深即为汇合不一致。RET 无后继，
// JMP 无落空；非跳转指令落空到长度处为落空越界。
//
// 返回的 entry 为每条指令的入口栈深，不可达指令为 -1；maxDepth 是可达
// 指令执行后栈深的最大值（不小于 0）。
func Analyze(limit int, ops []Op) (entry []int, maxDepth int, err *Error) {
	if limit < 1 {
		return nil, 0, newError(ReasonInvalidLimit, -1, -1, "stack limit must be >= 1")
	}
	if len(ops) == 0 {
		return nil, 0, newError(ReasonEmptyProgram, -1, -1, "instruction sequence is empty")
	}

	for pc, op := range ops {
		switch op.Code {
		case PUSH, POP, ADD, DUP, RET:
		case JMP, JZ:
		default:
			return nil, 0, newError(ReasonUnknownOp, pc, -1, "unknown opcode")
		}
	}

	n := len(ops)
	for pc, op := range ops {
		if op.Code == JMP || op.Code == JZ {
			if op.Target < 0 || op.Target >= n {
				return nil, 0, newError(ReasonJumpOutOfBounds, pc, -1, "jump target out of bounds")
			}
		}
	}

	entry = make([]int, n)
	for i := range entry {
		entry[i] = -1
	}
	queue := make([]int, 0, n)

	enqueue := func(pc, depth int) *Error {
		if entry[pc] == -1 {
			entry[pc] = depth
			queue = append(queue, pc)
			return nil
		}
		if entry[pc] != depth {
			return newError(ReasonMergeMismatch, pc, depth,
				"merge depth mismatch: existing entry depth differs")
		}
		return nil
	}
	enqueue(0, 0)

	maxDepth = 0
	for head := 0; head < len(queue); head++ {
		pc := queue[head]
		depth := entry[pc]
		op := ops[pc]

		required := 0
		switch op.Code {
		case POP, JZ:
			required = 1
		case ADD:
			required = 2
		case DUP:
			required = 1
		}
		if depth < required {
			return nil, 0, newError(ReasonUnderflow, pc, depth, "stack underflow")
		}

		post := depth
		switch op.Code {
		case PUSH, DUP:
			post = depth + 1
		case POP, JZ, ADD:
			post = depth - 1
		}
		if post > limit {
			return nil, 0, newError(ReasonOverflow, pc, depth, "stack depth exceeds limit")
		}

		if op.Code == RET && depth != 1 {
			return nil, 0, newError(ReasonBadReturnDepth, pc, depth, "RET requires entry depth exactly 1")
		}

		if post > maxDepth {
			maxDepth = post
		}

		switch op.Code {
		case RET:
		case JMP:
			if e := enqueue(op.Target, post); e != nil {
				return nil, 0, e
			}
		case JZ:
			if e := enqueue(op.Target, post); e != nil {
				return nil, 0, e
			}
			next := pc + 1
			if next == n {
				return nil, 0, newError(ReasonFallthroughBounds, pc, depth, "fallthrough runs past end of program")
			}
			if e := enqueue(next, post); e != nil {
				return nil, 0, e
			}
		default:
			next := pc + 1
			if next == n {
				return nil, 0, newError(ReasonFallthroughBounds, pc, depth, "fallthrough runs past end of program")
			}
			if e := enqueue(next, post); e != nil {
				return nil, 0, e
			}
		}
	}

	return entry, maxDepth, nil
}

// Validator 是并发安全的函数注册表。
type Validator struct {
	limit int
	mu    sync.RWMutex
	fns   map[string]int
}

// New 创建上限为 limit 的校验器。
func New(limit int) (*Validator, *Error) {
	if limit < 1 {
		return nil, newError(ReasonInvalidLimit, -1, -1, "stack limit must be >= 1")
	}
	return &Validator{limit: limit, fns: make(map[string]int)}, nil
}

// Register 注册一个函数。
//
// 注册与查询可并发调用；同名函数并发注册恰有一个成功。校验在调用方切片
// 的副本上进行，被拒绝的注册不会修改已注册函数表。
func (v *Validator) Register(name string, ops []Op) *Error {
	prog := append([]Op(nil), ops...)

	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.fns[name]; ok {
		return newError(ReasonNameExists, -1, -1, "function name already registered")
	}

	_, maxDepth, analyzeErr := Analyze(v.limit, prog)
	if analyzeErr != nil {
		return analyzeErr
	}
	v.fns[name] = maxDepth
	return nil
}

// MaxStack 查询已注册函数的最大栈深。
func (v *Validator) MaxStack(name string) (int, *Error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	depth, ok := v.fns[name]
	if !ok {
		return 0, newError(ReasonNotFound, -1, -1, "function not registered")
	}
	return depth, nil
}
