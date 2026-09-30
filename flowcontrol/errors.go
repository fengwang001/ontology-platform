package flowcontrol

import "errors"

// MaxWindow 是所有窗口（连接窗口与流窗口）允许的最大值 2^31-1。
const MaxWindow int64 = 1<<31 - 1

var (
	// ErrStreamNotFound：流编号从未开过。
	ErrStreamNotFound = errors.New("flowcontrol: stream not found")
	// ErrStreamClosed：流已经关闭。
	ErrStreamClosed = errors.New("flowcontrol: stream closed")
	// ErrNonPositive：n 或增量不是正数。
	ErrNonPositive = errors.New("flowcontrol: amount must be positive")
	// ErrFrameTooLarge：n 超过帧长上限 F。
	ErrFrameTooLarge = errors.New("flowcontrol: frame exceeds max frame size")
	// ErrConnWindow：n 超过连接窗口。
	ErrConnWindow = errors.New("flowcontrol: insufficient connection window")
	// ErrStreamWindow：n 超过流窗口（流窗口为负亦归此项）。
	ErrStreamWindow = errors.New("flowcontrol: insufficient stream window")
	// ErrInvalidWindow：构造参数或初始窗口非法（负或超过上限）。
	ErrInvalidWindow = errors.New("flowcontrol: invalid window")
	// ErrInvalidFrame：帧长上限 F 非正。
	ErrInvalidFrame = errors.New("flowcontrol: invalid max frame size")
	// ErrInvalidID：流编号非正。
	ErrInvalidID = errors.New("flowcontrol: stream id must be positive")
	// ErrIDUsed：流编号已用过（关闭后也不可复用）。
	ErrIDUsed = errors.New("flowcontrol: stream id already used")
	// ErrConnOverflow：增量后连接窗口超过 MaxWindow。
	ErrConnOverflow = errors.New("flowcontrol: connection window overflow")
	// ErrStreamOverflow：增量后流窗口超过 MaxWindow。
	ErrStreamOverflow = errors.New("flowcontrol: stream window overflow")
)
