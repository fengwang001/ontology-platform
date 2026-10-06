package charger

import "errors"

// 错误类别固定，拒绝次序见 DESIGN.md。
var (
	ErrInvalidArg   = errors.New("参数非法")
	ErrClockBack    = errors.New("时钟回退")
	ErrPortMissing  = errors.New("接口不存在")
	ErrPortOccupied = errors.New("接口占用")
	ErrNoSession    = errors.New("会话不存在")
	ErrWrongState   = errors.New("状态不允许")
)
