// Package pred 定义事件、无状态谓词与状态谓词（记录新高）。不依赖其他包。
package pred

import (
	"errors"
	"fmt"
)

// 可判定哨兵错误。
var (
	ErrBadPredicate = errors.New("pred: 非法谓词") // 空谓词或引用不存在的事件字段
	ErrBadEvent     = errors.New("pred: 非法事件") // Seq 非正或 Kind 为空串
)

// Event 是流水线上传递的事件，字段固定。
type Event struct {
	Seq  int64
	Val  int64
	Kind string
}

// Valid 校验事件合法性：Seq 必须为正，Kind 必须非空。
func (e Event) Valid() error {
	if e.Seq <= 0 {
		return fmt.Errorf("%w: Seq=%d 非正", ErrBadEvent, e.Seq)
	}
	if e.Kind == "" {
		return fmt.Errorf("%w: Seq=%d Kind 为空串", ErrBadEvent, e.Seq)
	}
	return nil
}

// Pred 是无状态谓词：对单条事件求值的纯布尔函数。
type Pred struct {
	Name  string
	Field string // 引用的字段："Seq"/"Val"/"Kind"；复合谓词为 ""
	F     func(Event) bool
}

// Valid 校验谓词合法性：空谓词或引用不存在的字段均为非法。
func (p Pred) Valid() error {
	if p.F == nil {
		return fmt.Errorf("%w: %q 是空谓词", ErrBadPredicate, p.Name)
	}
	switch p.Field {
	case "", "Seq", "Val", "Kind":
		return nil
	default:
		return fmt.Errorf("%w: %q 引用不存在的字段 %q", ErrBadPredicate, p.Name, p.Field)
	}
}

// Eval 对事件求值。
func (p Pred) Eval(e Event) bool { return p.F(e) }

// And 把两个无状态谓词合并为一个短路 AND 谓词。
func And(a, b Pred) Pred {
	return Pred{
		Name: a.Name + "&&" + b.Name,
		F:    func(e Event) bool { return a.F(e) && b.F(e) },
	}
}

// Even 是演示谓词 F1：Val 为偶数。
func Even() Pred {
	return Pred{Name: "F1", Field: "Val", F: func(e Event) bool { return e.Val%2 == 0 }}
}

// KindIs 是演示谓词 F2：Kind 等于给定值。
func KindIs(k string) Pred {
	return Pred{Name: "F2", Field: "Kind", F: func(e Event) bool { return e.Kind == k }}
}

// RecordHigh 是状态过滤器 S「记录新高」：
// 事件到达时若 Val 严格大于内部 max 则通过，否则丢弃；
// 无论通过与否，随后都执行 max = max(max, Val)。
type RecordHigh struct {
	max     int64
	started bool
}

// Eval 求值并更新内部状态。
func (r *RecordHigh) Eval(e Event) bool {
	pass := !r.started || e.Val > r.max
	if !r.started || e.Val > r.max {
		r.max = e.Val
		r.started = true
	}
	return pass
}

// Max 返回当前内部 max；尚未见过任何事件时 ok=false。
func (r *RecordHigh) Max() (max int64, ok bool) { return r.max, r.started }
