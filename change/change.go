package change

import "errors"

// Op 是变更操作类型。
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// Change 描述一条基表变更。Group 允许为空串；Update 时 OldGroup/OldValue 有效。
type Change struct {
	Ver      uint64
	Op       Op
	ID       string
	Group    string
	Value    float64
	OldGroup string
	OldValue float64
}

var (
	// ErrMissingID 表示记录 ID 缺失。
	ErrMissingID = errors.New("change: missing record id")
	// ErrNaN 表示值为 NaN，拒绝。
	ErrNaN = errors.New("change: value is NaN")
	// ErrBadOp 表示操作类型非法。
	ErrBadOp = errors.New("change: bad op")
)

// Valid 做入口校验：ID 必填、值不得为 NaN、操作必须合法。
func (c Change) Valid() error {
	if c.ID == "" {
		return ErrMissingID
	}
	if isNaN(c.Value) || (c.Op == Update && isNaN(c.OldValue)) {
		return ErrNaN
	}
	if c.Op != Insert && c.Op != Delete && c.Op != Update {
		return ErrBadOp
	}
	return nil
}

// EqualKey 比较除版本外的业务字段是否逐字段相同（用于幂等断言）。
func (c Change) EqualKey(o Change) bool {
	return c.Op == o.Op && c.ID == o.ID && c.Group == o.Group &&
		floatBits(c.Value) == floatBits(o.Value) &&
		c.OldGroup == o.OldGroup && floatBits(c.OldValue) == floatBits(o.OldValue)
}
