package trustanchor

import "fmt"

// ErrorKind 区分 Observe / NewTracker 的拒绝类别。
type ErrorKind int

const (
	// ErrInvalidConfig 构造参数非法，整体拒绝。
	ErrInvalidConfig ErrorKind = iota
	// ErrInvalidArgument Observe 参数非法（密钥集、签名者或 now 越界）。
	ErrInvalidArgument
	// ErrClockRollback now 小于此前被接受观测的最大 now。
	ErrClockRollback
	// ErrUntrustedSigners 去重后 VALID 签名者个数小于 Q。
	ErrUntrustedSigners
	// ErrLockup 处理后 VALID 个数小于 Q。
	ErrLockup
	// ErrOverflow 处理后被跟踪密钥数超过 Kmax。
	ErrOverflow
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidConfig:
		return "invalid config"
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRollback:
		return "clock rollback"
	case ErrUntrustedSigners:
		return "untrusted signers"
	case ErrLockup:
		return "lockup"
	case ErrOverflow:
		return "overflow"
	}
	return "unknown"
}

// Error 携带拒绝类别与定位信息。
type Error struct {
	Kind   ErrorKind
	Detail string
	Index  int    // 出错的密钥集项或签名者下标，无则为 -1
	ID     string // 出错位置对应的密钥标识（若有）
	Now    int64  // 本次观测的 now（时钟回退时有效）
	MaxNow int64  // 此前被接受观测的最大 now（时钟回退时有效）
	Have   int    // 实际数量（签名者/VALID/跟踪数）
	Need   int    // 需要的阈值（Q 或 Kmax）
}

func (e *Error) Error() string {
	return fmt.Sprintf("trustanchor: %s: %s", e.Kind, e.Detail)
}

// KindOf 提取错误的类别；err 不是 *Error 时返回 false。
func KindOf(err error) (ErrorKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func newError(kind ErrorKind, detail string) *Error {
	return &Error{Kind: kind, Detail: detail, Index: -1}
}
