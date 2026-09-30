package lsm

import "errors"

var (
	// ErrEmptyKey 表示写入了空键。
	ErrEmptyKey = errors.New("lsm: empty key")
	// ErrInvalidArgument 表示配置或参数非法。
	ErrInvalidArgument = errors.New("lsm: invalid argument")
	// ErrCorruptSegment 表示段文件内容损坏（校验失败或记录非法）。
	ErrCorruptSegment = errors.New("lsm: corrupt segment")
	// ErrClosed 表示存储已关闭。
	ErrClosed = errors.New("lsm: store closed")
)
