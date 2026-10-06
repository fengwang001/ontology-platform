package occupancy

import "fmt"

// ErrorCode 是可区分的拒绝原因。取值次序即报错优先级：
// 一次操作只报次序最靠前的一类。
type ErrorCode int

const (
	// ErrInvalidParams 参数非法。
	ErrInvalidParams ErrorCode = iota + 1
	// ErrClockRollback 操作时刻早于上一次被接受操作的时刻。
	ErrClockRollback
	// ErrRoadNotFound 路段不存在。
	ErrRoadNotFound
	// ErrPermitNotFound 许可不存在。
	ErrPermitNotFound
	// ErrPermitEnded 许可已结束（已撤销/待重排/时段已过），不可延期或撤销。
	ErrPermitEnded
	// ErrLanesExceed 封闭车道数超过路段车道数。
	ErrLanesExceed
	// ErrSameRoadConflict 同路段冲突；与绕行、走廊同时成立时也只报它。
	ErrSameRoadConflict
	// ErrDetourConflict 绕行冲突（两个方向共用）。
	ErrDetourConflict
	// ErrCorridorCap 走廊级并发封闭数达到上限。
	ErrCorridorCap
	// ErrStartBeforeNow 申请起始时刻早于操作时刻。
	ErrStartBeforeNow
)

var codeText = map[ErrorCode]string{
	ErrInvalidParams:    "invalid params",
	ErrClockRollback:    "clock rollback",
	ErrRoadNotFound:     "road not found",
	ErrPermitNotFound:   "permit not found",
	ErrPermitEnded:      "permit already ended",
	ErrLanesExceed:      "closed lanes exceed road lanes",
	ErrSameRoadConflict: "same-road conflict",
	ErrDetourConflict:   "detour conflict",
	ErrCorridorCap:      "corridor concurrency cap",
	ErrStartBeforeNow:   "start before operation time",
}

func (c ErrorCode) String() string {
	if t, ok := codeText[c]; ok {
		return t
	}
	return fmt.Sprintf("error(%d)", int(c))
}

// CodeError 携带可区分错误码，实现 error。
type CodeError struct {
	Code   ErrorCode
	Detail string
}

// None 判断是否为空（操作通过）。
func (e CodeError) None() bool { return e.Code == 0 }

// IsCode 判断错误码。
func (e CodeError) IsCode(c ErrorCode) bool { return e.Code == c }

func (e CodeError) Error() string {
	if e.Detail == "" {
		return e.Code.String()
	}
	return e.Code.String() + ": " + e.Detail
}

func fail(code ErrorCode, detail string) CodeError {
	return CodeError{Code: code, Detail: detail}
}
