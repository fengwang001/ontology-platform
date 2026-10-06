package station

import "errors"

var (
	ErrInvalidParam = errors.New("参数非法")
	ErrClockBack    = errors.New("时钟回退")
	ErrPortMissing  = errors.New("接口不存在")
	ErrPortBusy     = errors.New("接口占用")
	ErrSessionGone  = errors.New("会话不存在")
	ErrState        = errors.New("状态不允许")
)
