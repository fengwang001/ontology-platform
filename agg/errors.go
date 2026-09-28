package agg

import "errors"

// 四类互不相同、可区分的拒绝原因。任何一次被拒都不会改变全局状态。
var (
	// ErrInvalidOperation 行的操作不是 add / withdraw。
	ErrInvalidOperation = errors.New("agg: invalid operation: only add or withdraw is allowed")
	// ErrEmptyGroup 行或部分聚合中的组名为空。
	ErrEmptyGroup = errors.New("agg: empty group name")
	// ErrWithdrawBeforeAdd 撤回会使某个值在批次内或全局中的行数变为负。
	ErrWithdrawBeforeAdd = errors.New("agg: withdraw of a row that does not exist")
	// ErrTooManyGroups 合并后存活组数超过全局上限。
	ErrTooManyGroups = errors.New("agg: number of groups exceeds limit")
)
