package taxipool

import (
	"errors"
	"fmt"
)

// ErrorKind 是错误类别，按需求规定的优先级从 1 开始编号。
// 规范列出的十类之后补充两类（放行中不可离队、不在队列中），判定顺序位于十类之后。
type ErrorKind int

const (
	KindInvalidParam ErrorKind = iota + 1
	KindClockRewind
	KindTerminalNotFound
	KindDriverNotFound
	KindDriverBanned
	KindAlreadyQueued
	KindQueueFull
	KindNoTaxi
	KindNotDispatched
	KindArrivalOverdue
	KindCannotLeaveDispatched
	KindNotInQueue
)

// 哨兵错误，可用 errors.Is 直接判定类别。
var (
	ErrInvalidParam          = errors.New("参数非法")
	ErrClockRewind           = errors.New("时钟回退")
	ErrTerminalNotFound      = errors.New("候机楼不存在")
	ErrDriverNotFound        = errors.New("司机不存在")
	ErrDriverBanned          = errors.New("司机禁入中")
	ErrAlreadyQueued         = errors.New("司机已在队列中")
	ErrQueueFull             = errors.New("队列已满")
	ErrNoTaxi                = errors.New("无车可放行")
	ErrNotDispatched         = errors.New("司机未处于放行中")
	ErrArrivalOverdue        = errors.New("到达已逾期")
	ErrCannotLeaveDispatched = errors.New("放行后到达之前不可离队")
	ErrNotInQueue            = errors.New("司机不在队列中")
)

var kindSentinel = map[ErrorKind]error{
	KindInvalidParam:          ErrInvalidParam,
	KindClockRewind:           ErrClockRewind,
	KindTerminalNotFound:      ErrTerminalNotFound,
	KindDriverNotFound:        ErrDriverNotFound,
	KindDriverBanned:          ErrDriverBanned,
	KindAlreadyQueued:         ErrAlreadyQueued,
	KindQueueFull:             ErrQueueFull,
	KindNoTaxi:                ErrNoTaxi,
	KindNotDispatched:         ErrNotDispatched,
	KindArrivalOverdue:        ErrArrivalOverdue,
	KindCannotLeaveDispatched: ErrCannotLeaveDispatched,
	KindNotInQueue:            ErrNotInQueue,
}

// Sentinel 返回错误类别对应的哨兵错误。
func (k ErrorKind) Sentinel() error { return kindSentinel[k] }

// OpError 携带错误类别与上下文，Unwrap 指向对应哨兵错误。
type OpError struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *OpError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %v", e.Op, e.Kind.Sentinel())
	}
	return fmt.Sprintf("%s: %v（%s）", e.Op, e.Kind.Sentinel(), e.Detail)
}

func (e *OpError) Unwrap() error { return e.Kind.Sentinel() }

func opErr(op string, kind ErrorKind, detail string) *OpError {
	return &OpError{Kind: kind, Op: op, Detail: detail}
}

// KindOf 提取错误类别；非本包错误返回 0。
func KindOf(err error) ErrorKind {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Kind
	}
	for k, s := range kindSentinel {
		if errors.Is(err, s) {
			return k
		}
	}
	return 0
}
