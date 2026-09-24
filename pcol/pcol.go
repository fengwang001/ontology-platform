// Package pcol 定义列值类型、单条事件的格式校验，以及两条事件的合并与无变化列剔除。
package pcol

import "errors"

// 可判定的哨兵错误，四者互不相同。
var (
	ErrBadColumn      = errors.New("pcol: invalid column set")
	ErrNoSuchKey      = errors.New("pcol: key does not exist")
	ErrKeyExists      = errors.New("pcol: key already exists")
	ErrBeforeMismatch = errors.New("pcol: before value mismatch")
)

// Value 区分显式 NULL 与字符串；缺席用 map 中无键表示。
type Value struct {
	Null bool
	S    string
}

// Str 构造字符串值（空串是字符串，不是 NULL）。
func Str(s string) Value { return Value{S: s} }

// Null 构造显式 NULL 值。
func Null() Value { return Value{Null: true} }

// Kind 是事件类型。
type Kind int

const (
	Insert Kind = iota
	Update
)

// Event 是一条变更事件。Update 的 Set 只含本次变更的列。
type Event struct {
	Kind   Kind
	Key    string
	Set    map[string]Value
	Before map[string]Value
}

// CheckFormat 校验单条事件的格式：未知列、Insert 全列、Update 的 Set/Before 列集合一致。
func CheckFormat(e Event, cols map[string]bool) error {
	for c := range e.Set {
		if !cols[c] {
			return ErrBadColumn
		}
	}
	for c := range e.Before {
		if !cols[c] {
			return ErrBadColumn
		}
	}
	switch e.Kind {
	case Insert:
		if len(e.Before) != 0 || len(e.Set) != len(cols) {
			return ErrBadColumn
		}
		for c := range cols {
			if _, ok := e.Set[c]; !ok {
				return ErrBadColumn
			}
		}
	case Update:
		if len(e.Set) == 0 || len(e.Set) != len(e.Before) {
			return ErrBadColumn
		}
		for c := range e.Set {
			if _, ok := e.Before[c]; !ok {
				return ErrBadColumn
			}
		}
	default:
		return ErrBadColumn
	}
	return nil
}

// Merger 把事件逐条合并进已有合并结果。
// checked 记录最近一次 Merge 检查过的列数（含剔除检查），非导出，不进公开接口。
type Merger struct {
	checked int
}

// Merge 把 e 合并进 acc（原地修改 acc 的 map 并返回）。
// 只触及 e 涉及的列：同列取后到值，Before 保留首触值；
// 结果为 Update 时剔除 Set==Before 的列（NULL==NULL，NULL!=""）。
func (m *Merger) Merge(acc, e Event) Event {
	m.checked = 0
	for c, v := range e.Set {
		m.checked++
		if acc.Kind == Update {
			if _, ok := acc.Before[c]; !ok {
				acc.Before[c] = e.Before[c]
			}
		}
		acc.Set[c] = v
	}
	if acc.Kind == Update {
		for c := range e.Set {
			m.checked++
			if acc.Set[c] == acc.Before[c] {
				delete(acc.Set, c)
				delete(acc.Before, c)
			}
		}
	}
	return acc
}
