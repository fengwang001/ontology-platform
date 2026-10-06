// Package cors 实现跨源请求的预检判定与预检结果缓存内核。
//
// 内核由五部分协作构成：请求分类（简单/需预检）、预检必要性判定、
// 预检结果缓存、响应校验（预检响应与实际响应）与凭据模式处理。
// 同源请求不进入本内核，由调用方保证 origin != target（重定向产生的不透明
// 来源由内核生成，保证与任何目标都不相等）。
//
// 所有公开方法都持有同一把互斥锁，并发调用等价于某个串行顺序。
package cors

import "fmt"

// CredentialsMode 表示请求的凭据模式。
type CredentialsMode int

const (
	// CredentialsOmit 不携带凭据。
	CredentialsOmit CredentialsMode = iota
	// CredentialsInclude 携带凭据，此时响应中的各类通配声明均不生效。
	CredentialsInclude
)

func (m CredentialsMode) String() string {
	if m == CredentialsInclude {
		return "include"
	}
	return "omit"
}

// ErrorCode 是可区分的错误类别，数值顺序即拒绝优先级。
type ErrorCode int

const (
	// ErrCodeInvalidArgument 参数非法：空来源、空方法、头名字含非法字符、上限非正。
	ErrCodeInvalidArgument ErrorCode = iota
	// ErrCodeClockRollback 时钟回退。
	ErrCodeClockRollback
	// ErrCodeRedirectNotAllowed 预检请求遇到重定向。
	ErrCodeRedirectNotAllowed
	// ErrCodePreflightFailed 预检失败（含四种子原因，见 FailReason）。
	ErrCodePreflightFailed
	// ErrCodeResponseValidation 实际响应校验失败。
	ErrCodeResponseValidation
)

func (c ErrorCode) String() string {
	switch c {
	case ErrCodeInvalidArgument:
		return "invalid-argument"
	case ErrCodeClockRollback:
		return "clock-rollback"
	case ErrCodeRedirectNotAllowed:
		return "redirect-not-allowed"
	case ErrCodePreflightFailed:
		return "preflight-failed"
	case ErrCodeResponseValidation:
		return "response-validation-failed"
	}
	return "unknown"
}

// FailReason 是预检失败的子原因，数值顺序即报告优先级：
// 多个同时不满足时只报第一个（来源 > 凭据 > 方法 > 头）。
type FailReason int

const (
	FailNone FailReason = iota
	FailOrigin
	FailCredentials
	FailMethod
	FailHeader
)

func (r FailReason) String() string {
	switch r {
	case FailOrigin:
		return "origin"
	case FailCredentials:
		return "credentials"
	case FailMethod:
		return "method"
	case FailHeader:
		return "header"
	}
	return "none"
}

// Error 是内核返回的唯一错误类型，类别与子原因均可区分。
type Error struct {
	Code   ErrorCode
	Reason FailReason // 仅预检失败时非 FailNone
	Msg    string
}

func (e *Error) Error() string {
	if e.Reason != FailNone {
		return fmt.Sprintf("cors: %s (%s): %s", e.Code, e.Reason, e.Msg)
	}
	return fmt.Sprintf("cors: %s: %s", e.Code, e.Msg)
}

// Header 是一个请求头（名字任意大小写，内核一律按规范化小写处理）。
type Header struct {
	Name  string
	Value string
}

// Request 是一个跨源请求。
type Request struct {
	Origin      string // 发起来源；重定向后为内核生成的不透明来源
	Target      string // 目标来源（目标地址）
	Method      string
	Headers     []Header
	Credentials CredentialsMode
}

// HeaderRule 是单个安全头的值约束。
type HeaderRule struct {
	MaxLen  int             // 值长度上限（字节），必须为正
	Allowed func(byte) bool // 允许字符集谓词；nil 表示任意字节均可
}

// Config 是内核配置，全部上限必须为正（否则 NewKernel 报参数非法）。
type Config struct {
	SafeMethods         []string              // 安全方法集合（精确匹配）
	SafeHeaders         map[string]HeaderRule // 安全头集合及其值约束，键任意大小写
	SafeResponseHeaders []string              // 安全响应头集合（实际响应默认可暴露）
	MaxEntries          int                   // 缓存条目数上限，必须为正
	MaxAgeDefault       int64                 // 未声明存活时长时的默认值（时钟刻度数，>=0）
	MaxAgeMax           int64                 // 存活时长上限，必须为正
	// Logf 可选日志钩子：每次判定打印输入、输出与判定依据。
	Logf func(format string, args ...any)
}

// Action 是请求判定的结论。
type Action int

const (
	// ActionSend 直接发出（简单请求或缓存命中）。
	ActionSend Action = iota
	// ActionPreflight 必须先发起预检。
	ActionPreflight
)

func (a Action) String() string {
	if a == ActionPreflight {
		return "preflight"
	}
	return "send"
}

// Decision 是 Decide/SubmitRedirect 的输出。
type Decision struct {
	Action         Action
	CacheHit       bool     // 命中缓存而跳过预检
	Simple         bool     // 按简单请求直接发出
	NonSafeHeaders []string // 需预检覆盖的非安全头（规范化小写、去重）
	Reason         string   // 判定依据（人类可读）
}

// PreflightResponse 是服务端预检响应，头名字不区分大小写。
type PreflightResponse struct {
	Redirect bool              // 预检遇到重定向一律失败
	Headers  map[string]string // 原始响应头
}

// ActualResponse 是实际请求的响应，头名字不区分大小写。
type ActualResponse struct {
	Headers map[string]string
}
