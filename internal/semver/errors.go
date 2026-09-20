package semver

import "errors"

// ErrInvalidVersion 表示版本字符串不符合 SemVer 2.0.0 语法。
var ErrInvalidVersion = errors.New("semver: invalid version")

// ErrInvalidRange 表示范围约束字符串语法非法。
var ErrInvalidRange = errors.New("semver: invalid range constraint")

// ErrEmptyIntersection 表示多个范围求交后为空集。
var ErrEmptyIntersection = errors.New("semver: empty intersection")

// ConflictError 描述两条互相冲突的具体约束。
type ConflictError struct {
	// A 与 B 是两条冲突约束的原始写法（如 ">=1.2.3"、"^0.2.3"）。
	A string
	B string
}

func (e *ConflictError) Error() string {
	return "semver: constraints " + quote(e.A) + " and " + quote(e.B) +
		" conflict: intersection is empty"
}

// Unwrap 使 errors.Is(err, ErrEmptyIntersection) 成立。
func (e *ConflictError) Unwrap() error { return ErrEmptyIntersection }

func quote(s string) string { return `"` + s + `"` }
