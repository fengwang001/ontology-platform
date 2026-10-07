package servicemesh

import "fmt"

// ErrClass 为错误类别，数值越小优先级越高。
type ErrClass int

const (
	ClassInvalidArgument ErrClass = iota // 参数非法
	ClassVersionConflict                 // 版本冲突
	ClassValidation                      // 校验失败
	ClassNoRoute                         // 无路由
	ClassNoEndpoint                      // 无可用端点
)

// ValidationKind 为校验失败的子类，数值越小优先级越高。
type ValidationKind int

const (
	ValWeight   ValidationKind = iota // 分权重类
	ValPolicy                         // 策略类
	ValShadowed                       // 遮蔽类
)

// RouteError 为带类别、子类与规则序号的可区分错误。
type RouteError struct {
	Class   ErrClass
	Kind    ValidationKind
	RuleIdx int // 最小规则序号；兜底记为 -1
	Msg     string
}

func (e *RouteError) Error() string {
	return e.Msg
}

func errInvalid(format string, args ...any) *RouteError {
	return &RouteError{Class: ClassInvalidArgument, RuleIdx: -1, Msg: fmt.Sprintf(format, args...)}
}

func errConflict(current uint64) *RouteError {
	return &RouteError{Class: ClassVersionConflict, RuleIdx: -1,
		Msg: fmt.Sprintf("version conflict: expected base of current version %d", current)}
}

func errValidation(kind ValidationKind, ruleIdx int, format string, args ...any) *RouteError {
	return &RouteError{Class: ClassValidation, Kind: kind, RuleIdx: ruleIdx,
		Msg: fmt.Sprintf(format, args...)}
}

func errNoRoute(service string) *RouteError {
	return &RouteError{Class: ClassNoRoute, RuleIdx: -1,
		Msg: fmt.Sprintf("no route for service %q", service)}
}

func errNoEndpoint(service, subset string) *RouteError {
	return &RouteError{Class: ClassNoEndpoint, RuleIdx: -1,
		Msg: fmt.Sprintf("no ready endpoint for service %q subset %q", service, subset)}
}
