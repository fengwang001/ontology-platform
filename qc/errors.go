package qc

import "errors"

var (
	ErrInvalidArgument = errors.New("参数非法")
	ErrClockRollback   = errors.New("时钟回退")
	ErrNotFound        = errors.New("对象不存在")
	ErrOutOfControl    = errors.New("项目失控")
	ErrNeverRun        = errors.New("从未质控")
	ErrQcExpired       = errors.New("质控过期")
	ErrInvalidState    = errors.New("状态不符")
)
