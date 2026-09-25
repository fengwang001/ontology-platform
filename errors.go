package ontology

import "errors"

// ErrUnknownElement 表示操作引用了从未出现过的元素 ID。
// 调用方应使用 errors.Is(err, ErrUnknownElement) 判定。
var ErrUnknownElement = errors.New("ontology: unknown element")
