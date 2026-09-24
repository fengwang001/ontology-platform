// Package change 定义变更流中的单条变更记录及其编解码与校验。
package change

import (
	"encoding/json"
	"math"
)

// Op 为变更操作类型。
type Op uint8

const (
	Insert Op = 1 + iota
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
	default:
		return "invalid"
	}
}

// Change 是一条基表变更。
// Group 为分组键，HasGroup=false 表示缺失分组（非法，必须拒绝）；
// 空串 Group 配合 HasGroup=true 是合法分组。
// Ver 为单调递增版本号；ID 为记录标识。
// Insert/Delete 使用 New 作为该记录值；Update 时 Old→New。
type Change struct {
	Op       Op      `json:"op"`
	HasGroup bool    `json:"hasGroup"`
	Group    string  `json:"group"`
	Ver      int64   `json:"ver"`
	ID       string  `json:"id"`
	Old      float64 `json:"old"`
	New      float64 `json:"new"`
}

// Encode 返回 JSON 载荷（+0/-0 的位模式可由 JSON 往返保留）。
func Encode(c Change) ([]byte, error) { return json.Marshal(c) }

// Decode 解析 JSON 载荷。
func Decode(b []byte) (Change, error) {
	var c Change
	err := json.Unmarshal(b, &c)
	return c, err
}

// Validate 判定变更本身是否合法（与视图版本无关的静态校验）。
func (c Change) Validate() error {
	if c.Op != Insert && c.Op != Delete && c.Op != Update {
		return ErrBadOp
	}
	if !c.HasGroup {
		return ErrMissingGroup
	}
	if c.ID == "" {
		return ErrMissingID
	}
	if c.Ver <= 0 {
		return ErrBadVersion
	}
	if math.IsNaN(c.New) || math.IsNaN(c.Old) {
		return ErrNaN
	}
	return nil
}

// Value 返回该变更涉及的「新」值（Delete/Insert 的记录值，Update 的新值）。
func (c Change) Value() float64 { return c.New }

// SameValue 按 IEEE754 位级判等，因此 +0 与 -0 被视为相等，NaN 永不相等。
func SameValue(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	return math.Float64bits(a) == math.Float64bits(b) || (a == 0 && b == 0)
}

// Error 为 change 包的哨兵错误。
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrBadOp        Error = "change: invalid operation"
	ErrMissingGroup Error = "change: missing group key"
	ErrMissingID    Error = "change: missing record id"
	ErrBadVersion   Error = "change: non-positive version"
	ErrNaN          Error = "change: NaN value is rejected"
)
