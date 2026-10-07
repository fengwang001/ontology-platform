package ontology

// 错误按固定次序判定，只报第一类。判定次序不允许随内部实现变化：
//  1. ErrStartNotFound   起始对象不存在
//  2. ErrEmptyLabels     调用方权限标签集合为空
//  3. ErrInvalidDepth    给定的深度上限非正整数
//  4. ErrStartNotVisible 起始对象对调用方完全不可见

import "errors"

var (
	// ErrStartNotFound 起始对象在完整图中不存在。
	ErrStartNotFound = errors.New("start object not found")
	// ErrEmptyLabels 调用方被授予的权限标签集合为空。
	ErrEmptyLabels = errors.New("caller permission label set is empty")
	// ErrInvalidDepth 深度上限不是正整数。
	ErrInvalidDepth = errors.New("depth limit must be a positive integer")
	// ErrStartNotVisible 起始对象对调用方完全不可见（无任何可见链接与之关联）。
	ErrStartNotVisible = errors.New("start object is not visible to caller")
)
