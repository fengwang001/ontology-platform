package source

import (
	"errors"
	"fmt"
)

// 可判定的来源层错误，均可用 errors.Is 区分。
var (
	ErrEmptyKey      = errors.New("source: empty config key")
	ErrEmptySegment  = errors.New("source: empty segment in key path")
	ErrTruncated     = errors.New("source: truncated config file")
	ErrAmbiguousEnv  = errors.New("source: ambiguous environment variable name")
	ErrBadLine       = errors.New("source: malformed line (expected key=value)")
	ErrUnclosedRef   = errors.New("source: unclosed reference")
)

// TruncKind 描述文件截断落在最后一行的哪个部位。
type TruncKind string

const (
	TruncLine  TruncKind = "line incomplete"
	TruncKey   TruncKind = "key incomplete"
	TruncValue TruncKind = "value incomplete"
)

// TruncatedError 附带截断分类与行号（行从 1 起）。
type TruncatedError struct {
	Kind TruncKind
	Line int
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("%v: %s at line %d", ErrTruncated, e.Kind, e.Line)
}

func (e *TruncatedError) Unwrap() error { return ErrTruncated }

// AmbiguousEnvError 列出映射到同一配置键的不同环境变量名。
type AmbiguousEnvError struct {
	Key   string
	Names []string
}

func (e *AmbiguousEnvError) Error() string {
	return fmt.Sprintf("%v: key %q reached from %v", ErrAmbiguousEnv, e.Key, e.Names)
}

func (e *AmbiguousEnvError) Unwrap() error { return ErrAmbiguousEnv }
