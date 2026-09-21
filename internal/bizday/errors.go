package bizday

import "errors"

var (
	// ErrInvalidDate 表示日期非法（越界、不存在的月日等）。
	ErrInvalidDate = errors.New("bizday: invalid date")
	// ErrNotBusinessDay 表示 AddBusinessDays(d, 0) 中 d 不是工作日。
	ErrNotBusinessDay = errors.New("bizday: not a business day")
	// ErrInvalidRange 表示 CountBusinessDays 中 from > to。
	ErrInvalidRange = errors.New("bizday: from must not be after to")
)
