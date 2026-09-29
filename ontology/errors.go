// Package ontology 实现双流区间连接器（interval stream join）。
package ontology

import "errors"

// 可区分的拒绝原因。拒绝类错误均可通过 errors.Is 判定到下列哨兵错误。
var (
	// ErrInvalidArgument 配置参数非法（如下界大于上界、容量非正）。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrEmptyKey 连接键为空。
	ErrEmptyKey = errors.New("ontology: empty join key")
	// ErrTimeRegressed 单侧事件时间相对本侧水位线倒退。
	ErrTimeRegressed = errors.New("ontology: event time regressed")
	// ErrRetentionExceeded 一步处理并清理后，保留事件数仍超过容量上限。
	ErrRetentionExceeded = errors.New("ontology: retained state limit exceeded")
)
