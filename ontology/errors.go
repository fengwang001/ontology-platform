package ontology

import "errors"

var (
	// ErrInvalidRule 规则非法（空标识、区间非法、批次重复、删除不存在的规则等）。
	ErrInvalidRule = errors.New("invalid rule change")
	// ErrDeliveryOutOfRange 投递越界：实例编号为负。
	ErrDeliveryOutOfRange = errors.New("delivery target instance out of range")
	// ErrNegativeKey 数据键为负。
	ErrNegativeKey = errors.New("data key must be non-negative")
	// ErrBufferFull 目标实例的版本缓冲区已满。
	ErrBufferFull = errors.New("instance buffer full")
	// ErrNoPendingVersion 指定实例已是最新版本，没有可投递的新版本。
	ErrNoPendingVersion = errors.New("no pending version for instance")
)
