// Package stackcheck 提供字节码函数的栈深静态校验：
// 逐函数注册指令序列，按 FIFO 工作表做数据流推导，
// 保证通过校验的函数在任意控制流路径上栈深一致、
// 不下溢且不超过构造时给定的上限。
package stackcheck

import (
	"fmt"
	"sync"
)

// Op 表示指令操作码。
type Op int

const (
	OpPush Op = iota // 栈深 +1
	OpPop            // 栈深 -1，入口至少 1
	OpAdd            // 弹二压一，入口至少 2，净 -1
	OpDup            // 复制栈顶，入口至少 1，栈深 +1
	OpJmp            // 无条件跳转 Arg，无落空
	OpJz             // 弹一个（入口至少 1），再向 Arg 与下一条两路继续
	OpRet            // 返回，要求入口栈深恰为 1，无后继
)

// Instr 为一条指令；Arg 仅对 OpJmp / OpJz 有意义，表示目标下标。
type Instr struct {
	Op  Op
	Arg int
}

// ErrorKind 区分校验失败/查询失败的原因。
type ErrorKind int

const (
	_ ErrorKind = iota
	// KindLimitTooSmall 构造时栈上限小于 1。
	KindLimitTooSmall
	// KindNameExists 注册时函数名已存在。
	KindNameExists
	// KindEmptyCode 注册时指令序列为空。
	KindEmptyCode
	// KindUnknownOpcode 指令序列含未知操作码（报最小下标）。
	KindUnknownOpcode
	// KindJumpTargetOutOfRange 跳转目标不在 [0, len-1]（报最小下标）。
	KindJumpTargetOutOfRange
	// KindUnderflow 数据流推导中指令入口栈深不足。
	KindUnderflow
	// KindOverflow 数据流推导中指令执行后栈深超过上限。
	KindOverflow
	// KindMergeMismatch 后继入口栈深与已有值不一致。
	KindMergeMismatch
	// KindRetDepth RET 入口栈深不为 1。
	KindRetDepth
	// KindFallthroughOutOfRange 落空到下标等于指令总数处。
	KindFallthroughOutOfRange
	// KindFunctionNotFound 查询的函数不存在。
	KindFunctionNotFound
)

// Error 为可区分的校验/查询错误。
type Error struct {
	Kind  ErrorKind
	Name  string // 相关函数名（如适用）
	Index int    // 相关指令下标（如适用，否则 -1）
	Depth int    // 相关栈深（如适用，否则 -1）
	Limit int    // 相关上限/已有值（如适用，否则 -1）
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindLimitTooSmall:
		return fmt.Sprintf("stackcheck: 栈上限 %d 非法（须 >= 1）", e.Limit)
	case KindNameExists:
		return fmt.Sprintf("stackcheck: 函数 %q 已存在", e.Name)
	case KindEmptyCode:
		return fmt.Sprintf("stackcheck: 函数 %q 指令序列为空", e.Name)
	case KindUnknownOpcode:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处为未知操作码", e.Name, e.Index)
	case KindJumpTargetOutOfRange:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处跳转目标越界", e.Name, e.Index)
	case KindUnderflow:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处入口栈深 %d 不足（下溢）", e.Name, e.Index, e.Depth)
	case KindOverflow:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处执行后栈深 %d 超过上限 %d", e.Name, e.Index, e.Depth, e.Limit)
	case KindMergeMismatch:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处汇合栈深不一致（已有 %d，新到 %d）", e.Name, e.Index, e.Limit, e.Depth)
	case KindRetDepth:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处 RET 入口栈深为 %d，须恰为 1", e.Name, e.Index, e.Depth)
	case KindFallthroughOutOfRange:
		return fmt.Sprintf("stackcheck: 函数 %q 下标 %d 处落空越界（下标等于指令总数）", e.Name, e.Index)
	case KindFunctionNotFound:
		return fmt.Sprintf("stackcheck: 函数 %q 不存在", e.Name)
	default:
		return "stackcheck: 未知错误"
	}
}

// Validator 校验并记录已注册函数；可并发使用。
type Validator struct {
	limit int
	mu    sync.Mutex
	funcs map[string]int // 函数名 -> 最大栈深，注册成功后不再改变
}

func newErr(kind ErrorKind, name string) *Error {
	return &Error{Kind: kind, Name: name, Index: -1, Depth: -1, Limit: -1}
}

// NewValidator 构造栈上限为 limit 的校验器；limit 小于 1 时报错。
func NewValidator(limit int) (*Validator, error) {
	if limit < 1 {
		err := newErr(KindLimitTooSmall, "")
		err.Limit = limit
		return nil, err
	}
	return &Validator{limit: limit, funcs: make(map[string]int)}, nil
}

// Limit 返回构造时给定的栈上限。
func (v *Validator) Limit() int { return v.limit }

// Register 校验并注册函数；失败时不改变已注册函数表。
//
// 检查顺序：名字已存在 -> 指令序列为空 -> 未知操作码（最小下标）
// -> 跳转目标越界（最小下标，含不可达指令）-> FIFO 数据流推导
// （下溢 -> 超限 -> RET 栈深 -> 汇合/落空，按推导中先遇到者报）。
func (v *Validator) Register(name string, code []Instr) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.funcs[name]; ok {
		return newErr(KindNameExists, name)
	}
	if len(code) == 0 {
		return newErr(KindEmptyCode, name)
	}
	for i, in := range code {
		if in.Op < OpPush || in.Op > OpRet {
			err := newErr(KindUnknownOpcode, name)
			err.Index = i
			return err
		}
	}
	for i, in := range code {
		if in.Op == OpJmp || in.Op == OpJz {
			if in.Arg < 0 || in.Arg >= len(code) {
				err := newErr(KindJumpTargetOutOfRange, name)
				err.Index = i
				return err
			}
		}
	}
	maxDepth, err := verify(name, code, v.limit)
	if err != nil {
		return err
	}
	v.funcs[name] = maxDepth
	return nil
}

// MaxDepth 查询已注册函数的最大栈深。
func (v *Validator) MaxDepth(name string) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	maxDepth, ok := v.funcs[name]
	if !ok {
		return 0, newErr(KindFunctionNotFound, name)
	}
	return maxDepth, nil
}

// verify 按 FIFO 工作表做数据流推导，返回最大栈深（可达指令执行后
// 栈深的最大值，且不小于 0）。RET 执行后函数帧退出，记栈深 0。
func verify(name string, code []Instr, limit int) (int, error) {
	n := len(code)
	depths := make([]int, n)
	for i := range depths {
		depths[i] = -1 // -1 表示尚无入口栈深
	}
	depths[0] = 0
	queue := []int{0}
	maxDepth := 0

	assign := func(succ, depth int) error {
		switch {
		case depths[succ] == -1:
			depths[succ] = depth
			queue = append(queue, succ)
		case depths[succ] != depth:
			err := newErr(KindMergeMismatch, name)
			err.Index = succ
			err.Depth = depth
			err.Limit = depths[succ]
			return err
		}
		return nil
	}

	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		d := depths[i]
		in := code[i]

		out := d
		switch in.Op {
		case OpPush:
			out = d + 1
		case OpPop:
			if d < 1 {
				return 0, underflowErr(name, i, d)
			}
			out = d - 1
		case OpAdd:
			if d < 2 {
				return 0, underflowErr(name, i, d)
			}
			out = d - 1
		case OpDup:
			if d < 1 {
				return 0, underflowErr(name, i, d)
			}
			out = d + 1
		case OpJmp:
			out = d
		case OpJz:
			if d < 1 {
				return 0, underflowErr(name, i, d)
			}
			out = d - 1
		case OpRet:
			if d != 1 {
				err := newErr(KindRetDepth, name)
				err.Index = i
				err.Depth = d
				return 0, err
			}
			out = 0
		}
		if out > limit {
			err := newErr(KindOverflow, name)
			err.Index = i
			err.Depth = out
			err.Limit = limit
			return 0, err
		}
		if out > maxDepth {
			maxDepth = out
		}

		switch in.Op {
		case OpPush, OpPop, OpAdd, OpDup:
			if i+1 == n {
				err := newErr(KindFallthroughOutOfRange, name)
				err.Index = i
				return 0, err
			}
			if err := assign(i+1, out); err != nil {
				return 0, err
			}
		case OpJmp:
			if err := assign(in.Arg, out); err != nil {
				return 0, err
			}
		case OpJz:
			if err := assign(in.Arg, out); err != nil {
				return 0, err
			}
			if i+1 == n {
				err := newErr(KindFallthroughOutOfRange, name)
				err.Index = i
				return 0, err
			}
			if err := assign(i+1, out); err != nil {
				return 0, err
			}
		case OpRet:
		}
	}
	return maxDepth, nil
}

func underflowErr(name string, index, depth int) *Error {
	err := newErr(KindUnderflow, name)
	err.Index = index
	err.Depth = depth
	return err
}
