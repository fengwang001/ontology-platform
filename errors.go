package ontology

import "errors"

// 可判定的错误类别。调用方使用 errors.Is 区分子类。
var (
	// ErrInvalidCursor 游标格式非法：伪造、截断、篡改或解密失败。
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrSessionInvalid 游标本身合法，但其所属会话不存在或已被显式失效。
	// 与 ErrInvalidCursor 是两个不同类别。
	ErrSessionInvalid = errors.New("session invalid or expired")
	// ErrInvalidLimit limit 为负数；与返回空页是可区分的两种结果。
	ErrInvalidLimit = errors.New("limit must not be negative")
)

func isCursorInvalid(err error) bool { return errors.Is(err, ErrInvalidCursor) }
