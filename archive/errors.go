package archive

// ErrorCode 是可区分的错误类别。每个被拒绝的操作返回恰好一个错误码，
// 按服务端既定优先级判定（见服务实现）。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota // 参数非法（优先级最高）
	ErrClockRollback                    // 时钟回退
	ErrNotFound                         // 卷或借阅人不存在
	ErrStateNotAllowed                  // 状态不允许
	ErrClearanceTooLow                  // 密级不足
	ErrBorrowerSuspended                // 借阅人暂停
	ErrVolumeLent                       // 卷已借出
	ErrRenewLimitOrWindow               // 续借超限或窗口未到/已过
	ErrActiveReservation                // 存在有效预约
)

func (e ErrorCode) String() string {
	switch e {
	case ErrInvalidArgument:
		return "InvalidArgument"
	case ErrClockRollback:
		return "ClockRollback"
	case ErrNotFound:
		return "NotFound"
	case ErrStateNotAllowed:
		return "StateNotAllowed"
	case ErrClearanceTooLow:
		return "ClearanceTooLow"
	case ErrBorrowerSuspended:
		return "BorrowerSuspended"
	case ErrVolumeLent:
		return "VolumeLent"
	case ErrRenewLimitOrWindow:
		return "RenewLimitOrWindow"
	case ErrActiveReservation:
		return "ActiveReservation"
	default:
		return "Unknown"
	}
}

// OpError 携带错误类别以及失败项下标（仅批量借阅使用，单卷操作为 -1）。
type OpError struct {
	Code  ErrorCode
	Index int
}

func (e *OpError) Error() string {
	if e == nil {
		return ""
	}
	if e.Index >= 0 {
		return e.Code.String() + " at index " + itoa(e.Index)
	}
	return e.Code.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0'+n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
