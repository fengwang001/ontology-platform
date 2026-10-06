package riderassess

import "errors"

// ErrorKind 用可程序化判别的类别标注每一类拒绝原因。
type ErrorKind int

const (
	KindInvalidArgument ErrorKind = iota
	KindClockRollback
	KindRiderNotFound
	KindEventNotFound
	KindAppealNotFound
	KindEventRevoked
	KindEventAlreadyAppealed
	KindAppealAlreadyRuled
	KindSatelliteEvent
	KindAppealWindowExpired
)

// RuleError 是所有被拒绝操作返回的错误类型。
// 每个操作只返回按规定次序命中的第一个错误，且拒绝不改任何状态。
type RuleError struct {
	Kind ErrorKind
	Msg  string
}

func (e *RuleError) Error() string { return e.Msg }

func newError(kind ErrorKind, msg string) *RuleError {
	return &RuleError{Kind: kind, Msg: msg}
}

// IsRuleError 判断 err 是否为系统拒绝错误并返回其类别。
func IsRuleError(err error) (ErrorKind, bool) {
	var re *RuleError
	if errors.As(err, &re) {
		return re.Kind, true
	}
	return 0, false
}

var _ error = (*RuleError)(nil)
