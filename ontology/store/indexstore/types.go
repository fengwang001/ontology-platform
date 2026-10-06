// Package indexstore 实现带唯一二级索引的主表存储，以及崩溃后基于水位的
// 索引追赶（catch-up）恢复服务。
//
// 核心不变量：
//   - 主表（table）与写入日志（WAL）同步提交，崩溃后与日志末尾一致；
//   - 唯一二级索引（index）与水位（watermark）独立持久化，水位允许落后，
//     但绝不超前；
//   - 唯一性判定永远以主表最新状态为准，开销 O(1)，与索引落后多少无关；
//   - 追赶完成后的索引与对主表完整重建的结果逐项相同。
package indexstore

import "fmt"

// ErrorCode 是可区分的错误类别，按优先级排序（见各常量注释）。
type ErrorCode int

const (
	// ErrInvalidArgument 参数非法（空主键、batch<=0 等），优先级最高。
	ErrInvalidArgument ErrorCode = iota
	// ErrLogGap 日志序号不连续：期望 k 却见到非 k 的序号。
	ErrLogGap
	// ErrIndexStale 索引尚未追赶到日志末尾，查询/自检暂不可用。
	ErrIndexStale
	// ErrPrimaryNotFound 按主键删除时主键不存在。
	ErrPrimaryNotFound
	// ErrUniqueConflict 非空二级键已被另一个主键占用。
	ErrUniqueConflict
)

// Error 携带错误类别与可精确复现的判定依据说明。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", codeName(e.Code), e.Msg)
}

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidArgument:
		return "invalid-argument"
	case ErrLogGap:
		return "log-gap"
	case ErrIndexStale:
		return "index-stale"
	case ErrPrimaryNotFound:
		return "primary-not-found"
	case ErrUniqueConflict:
		return "unique-conflict"
	default:
		return "unknown"
	}
}

func errf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// LogOp 为日志条目类型。
type LogOp uint8

const (
	LogUpsert LogOp = 1 // 写入（插入或更新）一行
	LogDelete LogOp = 2 // 删除一行
)

// LogEntry 是一条 WAL 记录。LSN 从 1 开始连续。
// OldSec 为该行本次变更前持有的二级键；NewSec 为变更后持有的二级键
// （删除时为空）。HasOld/HasNew 区分“记录了空二级键”与“未记录”。
type LogEntry struct {
	LSN    int
	Op     LogOp
	PK     string
	OldSec string
	HasOld bool
	NewSec string
	HasNew bool
}

// Row 为主表中的一行。Sec 为二级键，HasSec=false 表示二级键为空（不入索引）。
type Row struct {
	PK     string
	Sec    string
	HasSec bool
}

// DiffKind 为自检发现的不一致类别。
type DiffKind uint8

const (
	// DiffExtra 索引里多余的条目（主表已无该二级键或该指向关系不应存在）。
	DiffExtra DiffKind = 1
	// DiffMissing 主表有该非空二级键，索引缺失。
	DiffMissing DiffKind = 2
	// DiffWrongOwner 索引有该二级键，但指向错误主键。
	DiffWrongOwner DiffKind = 3
)

// Diff 描述一项索引与主表的不一致。
type Diff struct {
	Kind      DiffKind
	Sec       string
	GotOwner  string // 索引当前指向（DiffExtra/DiffWrongOwner）
	WantOwner string // 主表期望指向（DiffMissing/DiffWrongOwner）
}

// OpKind 为操作日志记录的操作类型。
type OpKind string

const (
	OpPut     OpKind = "put"
	OpDelete  OpKind = "delete"
	OpGet     OpKind = "get-by-pk"
	OpLookup  OpKind = "lookup-by-sec"
	OpCatchUp OpKind = "catch-up"
	OpVerify  OpKind = "verify"
	OpReopen  OpKind = "reopen"
)

// OpRecord 记录一条对外操作的输入、输出与判定依据，供随机测试与复现使用。
type OpRecord struct {
	Kind    OpKind
	PK      string
	Sec     string
	HasSec  bool
	Batch   int
	OK      bool
	Result  string
	Found   bool
	LSN     int
	Reached int
	Reason  string
	ErrCode string
}
