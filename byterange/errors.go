package byterange

import "errors"

// ErrMalformed 表示 Range 头存在语法错误。
var ErrMalformed = errors.New("byterange: malformed range header")

// ErrUnsatisfiable 表示语法合法但没有任何区间可满足。
var ErrUnsatisfiable = errors.New("byterange: no satisfiable range")
