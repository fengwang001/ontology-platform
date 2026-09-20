package semver

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidVersion 表示版本字符串不符合 SemVer 2.0.0 语法。
	ErrInvalidVersion = errors.New("semver: invalid version")
	// ErrInvalidRange 表示范围约束字符串无法解析。
	ErrInvalidRange = errors.New("semver: invalid range")
	// ErrEmptyRange 表示对空字符串解析范围。
	ErrEmptyRange = errors.New("semver: empty range expression")
)

// VersionParseError 携带出错的原始版本字符串，可用 errors.Is 判定为 ErrInvalidVersion。
type VersionParseError struct {
	Input  string
	Reason string
}

func (e *VersionParseError) Error() string {
	return fmt.Sprintf("semver: invalid version %q: %s", e.Input, e.Reason)
}

func (e *VersionParseError) Unwrap() error { return ErrInvalidVersion }

// RangeParseError 携带出错的原始范围字符串，可用 errors.Is 判定为 ErrInvalidRange。
type RangeParseError struct {
	Input  string
	Reason string
}

func (e *RangeParseError) Error() string {
	return fmt.Sprintf("semver: invalid range %q: %s", e.Input, e.Reason)
}

func (e *RangeParseError) Unwrap() error { return ErrInvalidRange }

// ConflictError 表示两条具体约束之间没有任何可同时满足的版本。
// Left 与 Right 是冲突约束的原始写法。
type ConflictError struct {
	Left  string
	Right string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("semver: constraints %s and %s conflict (empty intersection)", e.Left, e.Right)
}
