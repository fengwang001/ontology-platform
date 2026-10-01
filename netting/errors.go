package netting

import "errors"

var (
	// ErrInvalidArgument 表示参数非法（空 id/oid、cap 或 amt 越界等）。
	ErrInvalidArgument = errors.New("netting: invalid argument")
	// ErrDuplicateParty 表示参与方重复登记。
	ErrDuplicateParty = errors.New("netting: duplicate party")
	// ErrDuplicateOID 表示同一周期内义务编号重复。
	ErrDuplicateOID = errors.New("netting: duplicate obligation id")
	// ErrUnknownParty 表示义务引用了未登记的参与方。
	ErrUnknownParty = errors.New("netting: unknown party")
	// ErrSelfCounterparty 表示义务的付方与收方相同。
	ErrSelfCounterparty = errors.New("netting: self counterparty")
	// ErrCycleFull 表示当前周期义务数量已达上限。
	ErrCycleFull = errors.New("netting: cycle full")
)
