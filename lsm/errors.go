package lsm

// 可区分的哨兵错误。

// ErrInvalidArgument 参数不合法（如 nil options、阈值为 0）。
var ErrInvalidArgument = newError("lsm: invalid argument")

// ErrEmptyKey 写入或读取的键为空。
var ErrEmptyKey = newError("lsm: empty key")

// ErrSegmentTruncated 段尾部存在不完整记录（可安全截断恢复）。
var ErrSegmentTruncated = newError("lsm: segment truncated: incomplete trailing record")

// ErrSegmentCorrupt 段内容非法（魔数错、标志位非法、CRC 错），无法安全恢复。
var ErrSegmentCorrupt = newError("lsm: segment corrupt")

type kvError struct{ msg string }

func newError(msg string) error { return &kvError{msg: msg} }

func (e *kvError) Error() string { return e.msg }

func (e *kvError) Is(target error) bool {
	t, ok := target.(*kvError)
	return ok && t.msg == e.msg
}
