package presence

import "errors"

// 可区分的拒绝原因，按“参数非法 > 时钟回退 > 不存在 > 状态不允许”的次序只报第一个。
var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrInvalidLease      = errors.New("invalid lease duration")
	ErrInvalidStatus     = errors.New("invalid status")
	ErrClockRollback     = errors.New("clock rollback")
	ErrUserNotFound      = errors.New("user not found")
	ErrDeviceNotFound    = errors.New("device not found")
	ErrTooManyDevices    = errors.New("too many devices")
	ErrAlreadySubscribed = errors.New("already subscribed")
	ErrSubscribeSelf     = errors.New("cannot subscribe to self")
	ErrBlockSelf         = errors.New("cannot block self")
	ErrAlreadyBlocked    = errors.New("already blocked")
	ErrNotBlocked        = errors.New("not blocked")
	ErrAlreadyInvisible  = errors.New("invisibility already in requested state")
	ErrNoSuchViewer      = errors.New("viewer not found")
)
