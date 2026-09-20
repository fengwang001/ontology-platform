package headers

import "errors"

var (
	// ErrMalformed 表示头部文本存在语法错误。
	ErrMalformed = errors.New("headers: malformed header block")
	// ErrSingleValue 表示单值头出现了多次且取值不同。
	ErrSingleValue = errors.New("headers: single-value header repeated with different values")
)
