// Package cors 实现跨源请求的预检判定与预检结果缓存内核。
package cors

import "fmt"

// Origin 表示一个发起来源。重定向到另一来源后，来源变为不透明：
// 不透明来源与任何目标比较都不相等，且每个不透明来源有唯一编号。
type Origin struct {
	value  string
	opaque bool
	id     uint64
}

// NewOrigin 构造一个普通（非不透明）来源。
func NewOrigin(value string) Origin { return Origin{value: value} }

func opaqueOrigin(id uint64) Origin { return Origin{opaque: true, id: id} }

// IsOpaque 报告来源是否不透明。
func (o Origin) IsOpaque() bool { return o.opaque }

// Value 返回来源字面值；不透明来源返回空串。
func (o Origin) Value() string { return o.value }

// Equal 判定两个来源是否相等；任一方不透明则恒不相等。
func (o Origin) Equal(other Origin) bool {
	if o.opaque || other.opaque {
		return false
	}
	return o.value == other.value
}

// cacheKey 返回来源在缓存键中的形态；不透明来源用唯一编号区分。
func (o Origin) cacheKey() string {
	if o.opaque {
		return fmt.Sprintf("\x00opaque:%d", o.id)
	}
	return o.value
}

func (o Origin) String() string {
	if o.opaque {
		return fmt.Sprintf("opaque#%d", o.id)
	}
	return o.value
}

// Header 是一个名字/值对。
type Header struct {
	Name  string
	Value string
}

// Request 是一次进入内核的跨源请求。
type Request struct {
	Origin             Origin
	Target             string
	Method             string
	Headers            []Header
	IncludeCredentials bool
}

// HeaderRule 是安全头的值约束：值长度上限与允许字符集。
type HeaderRule struct {
	MaxLength    int
	AllowedChars string
}

// Config 是内核的可配置项。
type Config struct {
	SafeMethods         []string
	SafeHeaders         map[string]HeaderRule
	SafeResponseHeaders []string
	DefaultMaxAge       int64
	MaxMaxAge           int64
	Capacity            int
}

// PreflightResponse 是服务端对预检请求的响应。
type PreflightResponse struct {
	Redirected       bool
	AllowOrigin      string
	AllowCredentials bool
	AllowAnyMethod   bool
	AllowMethods     []string
	AllowAnyHeader   bool
	AllowHeaders     []string
	MaxAge           *int64
}

// ActualResponse 是服务端对实际请求的响应。
type ActualResponse struct {
	AllowOrigin      string
	AllowCredentials bool
	ExposeAnyHeader  bool
	ExposeHeaders    []string
	Headers          []Header
	RedirectTo       string
}

// DecisionKind 是请求判定的类别。
type DecisionKind int

const (
	DecisionSameOrigin DecisionKind = iota
	DecisionSimple
	DecisionCacheHit
	DecisionPreflightNeeded
)

func (k DecisionKind) String() string {
	switch k {
	case DecisionSameOrigin:
		return "same-origin"
	case DecisionSimple:
		return "simple"
	case DecisionCacheHit:
		return "cache-hit"
	case DecisionPreflightNeeded:
		return "preflight-needed"
	}
	return "unknown"
}

// Decision 是一次请求判定的结果，Detail 记录判定依据。
type Decision struct {
	Kind   DecisionKind
	Detail string
}

// ActualResult 是实际响应校验的结果：要么给出暴露头，要么给出重定向后续请求。
type ActualResult struct {
	Redirect bool
	FollowUp Request
	Exposed  []Header
}

// ErrorKind 是可区分的错误类别。
type ErrorKind int

const (
	ErrKindInvalidArgument ErrorKind = iota
	ErrKindClockRollback
	ErrKindRedirectNotAllowed
	ErrKindPreflightFailed
	ErrKindResponseValidationFailed
)

func (k ErrorKind) String() string {
	switch k {
	case ErrKindInvalidArgument:
		return "invalid-argument"
	case ErrKindClockRollback:
		return "clock-rollback"
	case ErrKindRedirectNotAllowed:
		return "redirect-not-allowed"
	case ErrKindPreflightFailed:
		return "preflight-failed"
	case ErrKindResponseValidationFailed:
		return "response-validation-failed"
	}
	return "unknown"
}

// PreflightFailReason 是预检失败的子原因。
type PreflightFailReason int

const (
	ReasonNone PreflightFailReason = iota
	ReasonOriginMismatch
	ReasonCredentialsMismatch
	ReasonMethodMismatch
	ReasonHeaderMismatch
)

func (r PreflightFailReason) String() string {
	switch r {
	case ReasonOriginMismatch:
		return "origin-mismatch"
	case ReasonCredentialsMismatch:
		return "credentials-mismatch"
	case ReasonMethodMismatch:
		return "method-mismatch"
	case ReasonHeaderMismatch:
		return "header-mismatch"
	}
	return "none"
}

// Error 是内核返回的错误，Kind 与 Reason 可区分。
type Error struct {
	Kind    ErrorKind
	Reason  PreflightFailReason
	Message string
}

func (e *Error) Error() string {
	if e.Reason != ReasonNone {
		return fmt.Sprintf("%s/%s: %s", e.Kind, e.Reason, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func invalidArg(format string, args ...any) *Error {
	return &Error{Kind: ErrKindInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func preflightErr(reason PreflightFailReason, format string, args ...any) *Error {
	return &Error{Kind: ErrKindPreflightFailed, Reason: reason, Message: fmt.Sprintf(format, args...)}
}
