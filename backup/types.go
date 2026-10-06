package backup

import "strings"

// 取值上限（均为非负整数，含端点）。
const (
	// MaxTime 是时刻上限：9999-12-31T23:59:59Z 的 Unix 秒。
	MaxTime int64 = 253402300799
	// MaxSize 是备份大小上限（约 1 EiB）。
	MaxSize int64 = 1 << 60
	// MaxRetentionCount 是单层保留数量上限。
	MaxRetentionCount = 1000
)

// Kind 区分全量与增量备份。
type Kind int

const (
	// Full 全量备份，没有父备份。
	Full Kind = iota
	// Incremental 增量备份，必须指定一个已登记的父备份。
	Incremental
)

// Policy 给出日、周、月三层各自的保留数量，取值 [0, MaxRetentionCount]。
// N 为零表示该层不保留任何备份。
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Reasons 是保留原因的位集合，可同时具备多种原因。
type Reasons uint8

const (
	// ReasonDaily 因日层当选代表而直接保留。
	ReasonDaily Reasons = 1 << iota
	// ReasonWeekly 因周层当选代表而直接保留。
	ReasonWeekly
	// ReasonMonthly 因月层当选代表而直接保留。
	ReasonMonthly
	// ReasonDependency 因被保留后代依赖而保留（依赖保护）。
	ReasonDependency
	// ReasonLegalHold 因自身或后代被法律保留而保留。
	ReasonLegalHold
)

// Direct 返回直接保留（三层代表）原因位。
func (r Reasons) Direct() Reasons {
	return r & (ReasonDaily | ReasonWeekly | ReasonMonthly)
}

// Has 判断原因位 b 是否全部置位。
func (r Reasons) Has(b Reasons) bool { return r&b == b }

func (r Reasons) String() string {
	if r == 0 {
		return "none"
	}
	var parts []string
	if r&ReasonDaily != 0 {
		parts = append(parts, "daily")
	}
	if r&ReasonWeekly != 0 {
		parts = append(parts, "weekly")
	}
	if r&ReasonMonthly != 0 {
		parts = append(parts, "monthly")
	}
	if r&ReasonDependency != 0 {
		parts = append(parts, "dependency")
	}
	if r&ReasonLegalHold != 0 {
		parts = append(parts, "legal-hold")
	}
	return strings.Join(parts, "|")
}

// ErrorKind 是可区分的错误类别，声明顺序即报告优先级：
// 一次操作命中多类错误时，只报次序最靠前的一类。
type ErrorKind int

const (
	// ErrInvalidArgument 参数非法（空标识、越界的大小/时刻、类型与父备份不匹配等）。
	ErrInvalidArgument ErrorKind = iota + 1
	// ErrClockRollback 时钟回退：时刻小于上一次被接受操作的时刻。
	ErrClockRollback
	// ErrDuplicateID 标识重复。
	ErrDuplicateID
	// ErrParentNotFound 父备份不存在。
	ErrParentNotFound
	// ErrTimeOrder 时序矛盾：子备份创建时刻早于父备份。
	ErrTimeOrder
	// ErrBackupNotFound 备份不存在。
	ErrBackupNotFound
	// ErrRetentionLimit 层保留数量越界（不在 [0, MaxRetentionCount]）。
	ErrRetentionLimit
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid-argument"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrDuplicateID:
		return "duplicate-id"
	case ErrParentNotFound:
		return "parent-not-found"
	case ErrTimeOrder:
		return "time-order"
	case ErrBackupNotFound:
		return "backup-not-found"
	case ErrRetentionLimit:
		return "retention-limit"
	default:
		return "unknown"
	}
}

// Error 是服务返回的错误，Kind 可区分错误类别。
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Message }

// RetainedEntry 描述一个被保留备份及其保留原因。
type RetainedEntry struct {
	ID      string
	Reasons Reasons
}

// Plan 是一次清理计划：可删除集合与每个保留备份的保留原因。
// 两个列表都按（创建时刻，标识）升序排列，顺序唯一确定。
type Plan struct {
	Deletable []string
	Retained  []RetainedEntry
}
