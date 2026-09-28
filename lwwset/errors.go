// Package lwwset 实现最后写入者胜出（LWW）的元素集合 CRDT。
package lwwset

import "errors"

// 各类非法输入对应的可区分错误原因，均可用 errors.Is 判定。
var (
	// ErrEmptyReplicaID 副本编号为空串。
	ErrEmptyReplicaID = errors.New("lwwset: 副本编号不能为空")
	// ErrNilSource 合并来源副本为 nil。
	ErrNilSource = errors.New("lwwset: 合并来源副本不能为 nil")
	// ErrEmptyElement 元素为空串。
	ErrEmptyElement = errors.New("lwwset: 元素不能为空串")
	// ErrNonPositiveTimestamp 时间戳非正数。
	ErrNonPositiveTimestamp = errors.New("lwwset: 时间戳必须为正整数")
	// ErrTooManyElements 记录元素数超过上限。
	ErrTooManyElements = errors.New("lwwset: 记录元素数超过上限")
	// ErrInvalidMaxElements 元素数上限配置非法。
	ErrInvalidMaxElements = errors.New("lwwset: 元素数上限必须为正整数")
)
