package archive

// ErrorCode 是可区分的错误分类。
type ErrorCode int

const (
	OK                 ErrorCode = iota
	ErrInvalidParam              // 参数非法
	ErrClockRollback             // 时钟回退
	ErrNotFound                  // 卷或借阅人不存在
	ErrState                     // 状态不允许
	ErrClearance                 // 密级不足
	ErrSuspended                 // 借阅人暂停
	ErrAlreadyLent               // 卷已借出
	ErrRenewNotAllowed           // 续借超限或窗口未到
	ErrReservation               // 存在有效预约
)

func (e ErrorCode) Error() string {
	switch e {
	case OK:
		return "ok"
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrNotFound:
		return "not_found"
	case ErrState:
		return "state_not_allowed"
	case ErrClearance:
		return "clearance_insufficient"
	case ErrSuspended:
		return "user_suspended"
	case ErrAlreadyLent:
		return "volume_already_lent"
	case ErrRenewNotAllowed:
		return "renew_not_allowed"
	case ErrReservation:
		return "active_reservation"
	default:
		return "unknown"
	}
}
