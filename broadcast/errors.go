package broadcast

import "errors"

var (
	// ErrInvalidRule 非法规则：标识为空或同一版本内规则标识重复。
	ErrInvalidRule = errors.New("broadcast: invalid rule (empty or duplicate rule id)")
	// ErrInstanceOutOfRange 投递越界：实例编号超出实例集合范围。
	ErrInstanceOutOfRange = errors.New("broadcast: instance index out of range")
	// ErrNegativeKey 数据键为负。
	ErrNegativeKey = errors.New("broadcast: negative data key")
	// ErrBufferFull 目标实例缓冲区已满，数据无法暂存。
	ErrBufferFull = errors.New("broadcast: instance buffer full")
	// ErrVersionOutOfRange 投递版本不是已发布版本或未按版本顺序投递。
	ErrVersionOutOfRange = errors.New("broadcast: deliver version out of order or unpublished")
)
