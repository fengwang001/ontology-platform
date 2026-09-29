package prefixsum

import "errors"

// Reason 描述一次被整体拒绝的操作的可区分原因。
type Reason string

const (
	// ReasonInvalidArgument 表示参数本身非法（如 nil 视图、非法配置）。
	ReasonInvalidArgument Reason = "invalid_argument"
	// ReasonKeyOutOfRange 表示键超出视图配置的 [MinKey, MaxKey] 闭区间。
	ReasonKeyOutOfRange Reason = "key_out_of_range"
	// ReasonKeyNotFound 表示查询或删除的键当前不存在。
	ReasonKeyNotFound Reason = "key_not_found"
	// ReasonTooManyKeys 表示插入会使存在键数量超过 MaxKeys。
	ReasonTooManyKeys Reason = "too_many_keys"
	// ReasonOverflow 表示参与求和的中间或结果值超出 int64 范围。
	ReasonOverflow Reason = "overflow"
)

// Error 携带机器可判定的拒绝原因与人类可读描述。
type Error struct {
	Reason Reason
	msg    string
}

func (e *Error) Error() string { return string(e.Reason) + ": " + e.msg }

func errInvalid(msg string) error  { return &Error{Reason: ReasonInvalidArgument, msg: msg} }
func errKeyRange(msg string) error { return &Error{Reason: ReasonKeyOutOfRange, msg: msg} }
func errNotFound(key int64) error {
	return &Error{Reason: ReasonKeyNotFound, msg: "key does not exist"}
}
func errTooMany(msg string) error { return &Error{Reason: ReasonTooManyKeys, msg: msg} }
func errOverflow(msg string) error {
	return &Error{Reason: ReasonOverflow, msg: msg}
}

// ErrorReason 返回错误对应的可区分原因；非本包错误返回 ReasonInvalidArgument。
func ErrorReason(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ReasonInvalidArgument
}
