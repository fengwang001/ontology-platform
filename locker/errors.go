package locker

import "errors"

// 操作被拒绝时返回的哨兵错误。错误优先级见设计说明。
var (
	ErrInvalidParam      = errors.New("invalid parameter")
	ErrClockRollback     = errors.New("clock rollback")
	ErrDuplicateTracking = errors.New("duplicate tracking number")
	ErrNoFittingCell     = errors.New("no cell large enough exists in the cabinet")
	ErrAllFittingBusy    = errors.New("all fitting cells are occupied")
	ErrNoCodeAvailable   = errors.New("no pickup code available")
	ErrCodeNotFound      = errors.New("pickup code does not exist or has expired")
	ErrParcelLocked      = errors.New("parcel is locked")
	ErrPhoneMismatch     = errors.New("phone last-four digits mismatch")
	ErrTimedOut          = errors.New("parcel has timed out")
	ErrUnpaidFee         = errors.New("storage fee is not fully paid")
	ErrTrackingNotFound  = errors.New("tracking number not found in the cabinet")
	ErrNotTimedOut       = errors.New("parcel has not timed out and cannot be recycled")
)
