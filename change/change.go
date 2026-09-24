package change

import (
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op uint8

const (
	Insert Op = iota + 1
	Delete
	Update
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	}
	return "unknown"
}

var (
	// ErrInvalidVersion 版本号非正。
	ErrInvalidVersion = errors.New("change: version must be positive")
	// ErrMissingGroup 新旧分组键都缺失。
	ErrMissingGroup = errors.New("change: missing group key")
	// ErrNaNValue 数值为 NaN。
	ErrNaNValue = errors.New("change: NaN value is not allowed")
	// ErrInvalidOp 操作类型与字段不匹配。
	ErrInvalidOp = errors.New("change: invalid operation or fields")
)

// Change 是一条基表变更。Insert 只填 New*，Delete 只填 Old*，Update 全填。
type Change struct {
	Op       Op      `json:"op"`
	Version  int64   `json:"v"`
	OldGroup string  `json:"og,omitempty"`
	NewGroup string  `json:"ng,omitempty"`
	OldValue float64 `json:"ov,omitempty"`
	NewValue float64 `json:"nv,omitempty"`
	// HasOld/HasNew 区分空串键与「未提供」。
	HasOld bool `json:"ho,omitempty"`
	HasNew bool `json:"hn,omitempty"`
}

// normZero 把 -0 归一为 +0。
func normZero(x float64) float64 {
	if math.Float64bits(x) == math.Float64bits(0) {
		return 0
	}
	return x
}

// Normalize 归一化正负零。
func (c *Change) Normalize() {
	c.OldValue = normZero(c.OldValue)
	c.NewValue = normZero(c.NewValue)
}

// Validate 校验变更并归一化。
func (c *Change) Validate() error {
	if c.Version <= 0 {
		return ErrInvalidVersion
	}
	switch c.Op {
	case Insert:
		if !c.HasNew || c.HasOld {
			return ErrInvalidOp
		}
	case Delete:
		if !c.HasOld || c.HasNew {
			return ErrInvalidOp
		}
	case Update:
		if !c.HasOld || !c.HasNew {
			return ErrInvalidOp
		}
	default:
		return ErrInvalidOp
	}
	if !c.HasOld && !c.HasNew {
		return ErrMissingGroup
	}
	if math.IsNaN(c.OldValue) || math.IsNaN(c.NewValue) {
		return ErrNaNValue
	}
	c.Normalize()
	return nil
}

// Groups 返回受影响的组（去重）与每条的 (key,value,isAdd) 增减动作。
func (c Change) Actions() []Action {
	var out []Action
	switch c.Op {
	case Insert:
		out = append(out, Action{c.NewGroup, c.NewValue, true})
	case Delete:
		out = append(out, Action{c.OldGroup, c.OldValue, false})
	case Update:
		if c.NewGroup == c.OldGroup && math.Float64bits(c.NewValue) == math.Float64bits(c.OldValue) {
			return nil
		}
		out = append(out, Action{c.OldGroup, c.OldValue, false})
		out = append(out, Action{c.NewGroup, c.NewValue, true})
	}
	return out
}

// Action 是对单个分组多重集的一次加/减。
type Action struct {
	Group string
	Value float64
	Add   bool
}
