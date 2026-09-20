package ontology

import (
	"errors"
	"fmt"
)

// 可判定的哨兵错误，均可用 errors.Is 判断。
var (
	// ErrSnapshotReleased 表示对已归还的快照进行读取。
	ErrSnapshotReleased = errors.New("snapshot already released")
	// ErrVersionReclaimed 表示快照或查询指向的版本已被回收。
	ErrVersionReclaimed = errors.New("version has been reclaimed")
	// ErrUnknownVersion 表示查询指向的版本号从未存在过。
	ErrUnknownVersion = errors.New("unknown version")
	// ErrUnknownField 表示更新或读取了未在 Schema 中声明的字段。
	ErrUnknownField = errors.New("unknown field")
	// ErrTypeMismatch 表示字段值类型与声明不符。
	ErrTypeMismatch = errors.New("field type mismatch")
	// ErrOutOfRange 表示整数字段超出声明的上下界。
	ErrOutOfRange = errors.New("integer field out of range")
)

// ValidationError 指出整次更新中某个字段未通过校验。
// Field 是出错的字段名，Err 是上述三类校验哨兵错误之一。
type ValidationError struct {
	Field string
	Err   error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %q: %v", e.Field, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }
