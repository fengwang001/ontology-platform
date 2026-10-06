// Package bcontext 实现浏览上下文树的跨源隔离与权限策略求值内核。
package bcontext

import (
	"fmt"
	"io"
	"log"
	"os"
)

// OpenerPolicy 为文档的开启者策略（Cross-Origin-Opener-Policy 抽象）。
type OpenerPolicy int

const (
	OpenerUnsafeNone            OpenerPolicy = iota // 无
	OpenerSameOrigin                                // 同源
	OpenerSameOriginAllowPopups                     // 同源允许弹窗
)

func (p OpenerPolicy) valid() bool {
	return p >= OpenerUnsafeNone && p <= OpenerSameOriginAllowPopups
}

func (p OpenerPolicy) String() string {
	switch p {
	case OpenerUnsafeNone:
		return "none"
	case OpenerSameOrigin:
		return "same-origin"
	case OpenerSameOriginAllowPopups:
		return "same-origin-allow-popups"
	default:
		return fmt.Sprintf("opener-policy(%d)", int(p))
	}
}

// EmbedderPolicy 为文档的嵌入者策略（Cross-Origin-Embedder-Policy 抽象）。
type EmbedderPolicy int

const (
	EmbedderUnsafeNone     EmbedderPolicy = iota // 无
	EmbedderRequireCorp                          // 要求凭据
	EmbedderCredentialless                       // 无凭据
)

func (p EmbedderPolicy) valid() bool {
	return p >= EmbedderUnsafeNone && p <= EmbedderCredentialless
}

// strict 表示该文档只可被同样声明了要求凭据或无凭据策略的跨源文档嵌入。
func (p EmbedderPolicy) strict() bool {
	return p == EmbedderRequireCorp || p == EmbedderCredentialless
}

func (p EmbedderPolicy) String() string {
	switch p {
	case EmbedderUnsafeNone:
		return "none"
	case EmbedderRequireCorp:
		return "require-corp"
	case EmbedderCredentialless:
		return "credentialless"
	default:
		return fmt.Sprintf("embedder-policy(%d)", int(p))
	}
}

// DefaultAllowlist 为能力的默认允许列表配置。
type DefaultAllowlist int

const (
	DefaultSelf DefaultAllowlist = iota // 仅自身来源
	DefaultAll                          // 全部来源
)

// Feature 描述一项能力及其全局配置。
type Feature struct {
	// RequiresIsolation 为真时该能力只能在跨源隔离文档中使用。
	RequiresIsolation bool
	// Default 为未显式声明时该能力的默认允许列表。
	Default DefaultAllowlist
}

// Document 为一次装载所确定的文档响应头与声明。
type Document struct {
	Origin   string
	Opener   OpenerPolicy
	Embedder EmbedderPolicy
	// Permissions 为文档自身的权限策略声明：能力名 -> 允许来源集合。
	// 显式出现（即使为空集合）表示该能力被声明；未出现表示未声明、走默认列表。
	Permissions map[string][]string
}

func (d Document) validate() error {
	if d.Origin == "" {
		return fmt.Errorf("%w: empty origin", ErrInvalidArgument)
	}
	if !d.Opener.valid() {
		return fmt.Errorf("%w: unknown opener policy %d", ErrInvalidArgument, int(d.Opener))
	}
	if !d.Embedder.valid() {
		return fmt.Errorf("%w: unknown embedder policy %d", ErrInvalidArgument, int(d.Embedder))
	}
	for feature, origins := range d.Permissions {
		if feature == "" {
			return fmt.Errorf("%w: empty feature name", ErrInvalidArgument)
		}
		for _, origin := range origins {
			if origin == "" {
				return fmt.Errorf("%w: empty allowed origin for feature %q", ErrInvalidArgument, feature)
			}
		}
	}
	return nil
}

// declaredSelfAllowed 返回文档自身声明是否允许自身来源使用该能力。
// 未声明时按该能力默认列表取值。
func (d Document) declaredSelfAllowed(feature string, def DefaultAllowlist) bool {
	origins, declared := d.Permissions[feature]
	if !declared {
		// 默认允许列表既决定父级委托范围，也决定文档自身是否默认获得能力：
		// 默认全部时任何文档默认可用；默认仅自身时文档自身来源默认可用。
		return true
	}
	for _, origin := range origins {
		if origin == d.Origin || origin == "*" {
			return true
		}
	}
	return false
}

// Logger 打印每次操作的输入、输出与判定依据。
type Logger struct {
	inner *log.Logger
}

// NewLogger 创建写入 w 的日志器；w 为 nil 时丢弃日志。
func NewLogger(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{inner: log.New(w, "[bcontext] ", log.LstdFlags|log.Lmicroseconds)}
}

func (l *Logger) logf(format string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	l.inner.Printf(format, args...)
}

// DefaultLogger 默认打印到标准错误。
var DefaultLogger = NewLogger(os.Stderr)
