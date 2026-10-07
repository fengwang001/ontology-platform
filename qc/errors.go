package qc

import "errors"

var (
	// ErrInvalidParam 参数非法：空标识、非正标准差/有效期、复核单号非正等。
	ErrInvalidParam = errors.New("qc: 参数非法")
	// ErrClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	ErrClockRollback = errors.New("qc: 时钟回退")
	// ErrNotFound 对象不存在：项目或报告未登记。
	ErrNotFound = errors.New("qc: 对象不存在")
	// ErrOutOfControl 项目失控：失控状态下拒绝出具报告。
	ErrOutOfControl = errors.New("qc: 项目失控")
	// ErrNeverTested 从未质控：项目尚无任何一次运行。
	ErrNeverTested = errors.New("qc: 从未质控")
	// ErrQCExpired 质控过期：距最近一次非失控运行严格晚于有效期。
	ErrQCExpired = errors.New("qc: 质控过期")
	// ErrBadState 状态不符：如对非待复核报告执行复核。
	ErrBadState = errors.New("qc: 状态不符")
)
