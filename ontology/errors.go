package ontology

import "errors"

// 存储层错误。
var (
	ErrLinkExists   = errors.New("ontology: link already exists")
	ErrLinkNotFound = errors.New("ontology: link not found")
)

// 遍历入口错误。四类错误互斥，按以下固定次序判定，只报告第一类：
//
//  1. ErrStartNotFound    起始对象不存在
//  2. ErrEmptyCallerLabels 调用方权限标签集合为空
//  3. ErrInvalidMaxDepth   给定的深度上限非正整数
//  4. ErrStartInvisible    起始对象对调用方完全不可见
var (
	ErrStartNotFound     = errors.New("ontology: start object does not exist")
	ErrEmptyCallerLabels = errors.New("ontology: caller label set is empty")
	ErrInvalidMaxDepth   = errors.New("ontology: max depth must be a positive integer")
	ErrStartInvisible    = errors.New("ontology: start object is invisible to caller")
)
