package ontology

import "errors"

var (
	// ErrInvalidLimit 表示 limit 为负数。它与“空页”是两种可区分的结果：
	// limit < 0 返回错误；limit == 0 返回空页且游标不前进。
	ErrInvalidLimit = errors.New("ontology: limit must not be negative")

	// ErrCursorMalformed 表示游标字符串本身不合法：伪造、截断、篡改或无法解码。
	ErrCursorMalformed = errors.New("ontology: malformed cursor")

	// ErrCursorInvalidated 表示游标格式合法，但其所属遍历会话已不存在
	// 或已被显式失效。与 ErrCursorMalformed 是不同的错误类别。
	ErrCursorInvalidated = errors.New("ontology: cursor session invalidated")
)
