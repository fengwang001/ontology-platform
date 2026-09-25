// Package pred 定义谓词：无状态谓词（纯函数）与状态谓词（记录新高）。
package pred

import (
	"errors"
	"fmt"
	"math"
)

// ErrBadPredicate 谓词非法：空谓词，或引用不存在的事件字段。
var ErrBadPredicate = errors.New("pred: invalid predicate")

// ErrBadEvent 事件非法：Seq 非正，或 Kind 为空串。
var ErrBadEvent = errors.New("pred: invalid event")

// Event 是流经流水线的事件，字段固定。
type Event struct {
	Seq  int64
	Val  int64
	Kind string
}

// Valid 校验事件合法性。
func (e Event) Valid() error {
	if e.Seq <= 0 || e.Kind == "" {
		return fmt.Errorf("%w: seq=%d kind=%q", ErrBadEvent, e.Seq, e.Kind)
	}
	return nil
}

// Field 是可被谓词引用的事件字段。
type Field int

const (
	fieldNone Field = iota // 零值：非法（空谓词）
	FieldVal
	FieldKind
)

type nodeKind int

const (
	nodeLeaf nodeKind = iota // 单字段比较
	nodeAnd                  // 合取
)

// Stateless 是无状态谓词：对单条事件求值的纯布尔函数。
// 零值是空谓词，Valid 报 ErrBadPredicate。
type Stateless struct {
	field Field
	kind  nodeKind
	even  bool   // field==FieldVal：谓词「Val 为偶数」
	arg   string // field==FieldKind：谓词 Kind==arg
	kids  []Stateless
}

// Even 构造谓词「Val 为偶数」（题面 F1）。
func Even() Stateless { return Stateless{field: FieldVal, even: true} }

// KindEq 构造谓词「Kind == s」（题面 F2 即 KindEq("A")）。
func KindEq(s string) Stateless { return Stateless{field: FieldKind, arg: s} }

// OfField 按字段编号构造（供故障注入：非法字段 → ErrBadPredicate）。
func OfField(f int, arg string) Stateless { return Stateless{field: Field(f), arg: arg} }

// And 把多个无状态谓词合并为一个短路 AND 谓词。
func And(ps ...Stateless) Stateless { return Stateless{kind: nodeAnd, kids: ps} }

// Valid 校验谓词合法性。
func (p Stateless) Valid() error {
	if p.kind == nodeAnd {
		if len(p.kids) == 0 {
			return fmt.Errorf("%w: empty conjunction", ErrBadPredicate)
		}
		for _, k := range p.kids {
			if err := k.Valid(); err != nil {
				return err
			}
		}
		return nil
	}
	switch p.field {
	case FieldVal:
		return nil
	case FieldKind:
		if p.arg == "" {
			return fmt.Errorf("%w: empty Kind operand", ErrBadPredicate)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown field %d", ErrBadPredicate, int(p.field))
	}
}

// Eval 对单条事件求值（纯函数，短路 AND）。
func (p Stateless) Eval(e Event) bool {
	if p.kind == nodeAnd {
		for _, k := range p.kids {
			if !k.Eval(e) {
				return false
			}
		}
		return true
	}
	if p.field == FieldVal {
		return e.Val%2 == 0
	}
	return e.Kind == p.arg
}

// HighWater 是状态谓词「记录新高」（题面 S）。
// 事件到达时若 Val > max 则通过；无论通过与否都执行 max = max(max, Val)。
type HighWater struct {
	max int64
}

// NewHighWater 构造 max 初值为负无穷的状态谓词。
func NewHighWater() *HighWater { return &HighWater{max: math.MinInt64} }

// Eval 求值并更新内部状态。
func (h *HighWater) Eval(e Event) bool {
	pass := e.Val > h.max
	if e.Val > h.max {
		h.max = e.Val
	}
	return pass
}

// Max 返回当前内部 max（只读）。
func (h *HighWater) Max() int64 { return h.max }
