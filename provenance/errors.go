package provenance

import "errors"

// 查询参数错误：四类互斥错误，判定次序固定（见 Query）。
var (
	// ErrSourceNotFound 源对象实例在存储中完全不存在（任何时间都没有记录）。
	ErrSourceNotFound = errors.New("provenance: source object instance does not exist")
	// ErrInvalidTime 给定的有效时间点或写入时间点为非法值。
	ErrInvalidTime = errors.New("provenance: invalid valid-at or as-of time")
	// ErrInvalidDepth 遍历深度上限不是正整数。
	ErrInvalidDepth = errors.New("provenance: max depth must be a positive integer")
	// ErrAsOfBeforeSource 写入时间点早于源对象自身最早的写入时间。
	ErrAsOfBeforeSource = errors.New("provenance: as-of time is earlier than the source's earliest write time")
)

// 写入修正错误。
var (
	// ErrInvalidInterval 修正携带的有效时间区间非法。
	ErrInvalidInterval = errors.New("provenance: invalid validity interval")
	// ErrOutOfOrderWrite 修正写入时间必须严格晚于该实体已有最新记录的写入时间。
	ErrOutOfOrderWrite = errors.New("provenance: correction write time must be strictly later than the prior record")
)

// InvisibleReason 区分路径不可见的两种业务类别。
type InvisibleReason int

const (
	// ReasonVisible 三方条件全部满足。
	ReasonVisible InvisibleReason = iota
	// ReasonNotEstablished 此刻未建立：在给定有效时间点不存在覆盖记录
	// （且不存在「写入时间更晚、却覆盖该有效时间点」的记录）。
	ReasonNotEstablished
	// ReasonNotYetVisible 已建立但对此查询尚不可见：存在覆盖该有效时间点的记录，
	// 但其写入时间晚于给定写入时间点。
	ReasonNotYetVisible
)

func (r InvisibleReason) String() string {
	switch r {
	case ReasonVisible:
		return "visible"
	case ReasonNotEstablished:
		return "not_established"
	case ReasonNotYetVisible:
		return "not_yet_visible"
	default:
		return "unknown"
	}
}
