package rebalance

import "errors"

var (
	// ErrInvalidArgument 表示入参本身非法（编号为负、成员名为空等）。
	ErrInvalidArgument = errors.New("rebalance: 非法参数")
	// ErrMemberExists 表示成员重复加入。
	ErrMemberExists = errors.New("rebalance: 成员已存在（重复加入）")
	// ErrMemberNotFound 表示操作针对的成员不在组内。
	ErrMemberNotFound = errors.New("rebalance: 成员不存在")
	// ErrNoRevocation 表示成员没有待确认的撤销中分区却调用了确认。
	ErrNoRevocation = errors.New("rebalance: 无待确认的撤销")
)
