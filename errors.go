package ontology

import (
	"errors"
	"fmt"
)

// 各类失败原因。被拒绝的操作可通过 errors.Is 与这些哨兵错误比较。
var (
	ErrInvalidTxn       = errors.New("事务号必须为正整数")
	ErrInvalidMode      = errors.New("非法的锁模式")
	ErrEmptyNodeID      = errors.New("节点 id 不能为空")
	ErrNodeExists       = errors.New("节点已登记")
	ErrParentNotExist   = errors.New("父节点未登记")
	ErrNodeNotExist     = errors.New("节点未登记")
	ErrMissingIntent    = errors.New("缺少祖先意向")
	ErrLockConflict     = errors.New("与其他事务的持锁冲突")
	ErrLockNotHeld      = errors.New("该事务未持有该节点上的锁")
	ErrDescendantLocked = errors.New("该事务在真后代上仍持有锁")
)

// AncestorIntentError 表示祖先意向不足。
// Ancestor 为从根往下数第一个不满足意向要求的祖先节点；
// Required 为该处所需要的祖先意向（IS 或 IX）。
type AncestorIntentError struct {
	Txn      int64
	Ancestor string
	Held     Mode // 该事务在 Ancestor 上实际持有的模式；未持有时为空串
	Required Mode
}

func (e *AncestorIntentError) Error() string {
	return fmt.Sprintf("事务 %d 在祖先 %q 上的模式 %q 不满足所需意向 %q",
		e.Txn, e.Ancestor, e.Held, e.Required)
}

func (e *AncestorIntentError) Unwrap() error { return ErrMissingIntent }

// ConflictError 表示目标模式与其他事务在同一节点上的持锁冲突。
// ConflictTxn / ConflictMode 为冲突事务中事务号最小者及其模式。
type ConflictError struct {
	Txn          int64
	Node         string
	Mode         Mode
	ConflictTxn  int64
	ConflictMode Mode
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("事务 %d 在节点 %q 上请求 %q 与事务 %d 的 %q 冲突",
		e.Txn, e.Node, e.Mode, e.ConflictTxn, e.ConflictMode)
}

func (e *ConflictError) Unwrap() error { return ErrLockConflict }

// DescendantLockError 表示 Unlock 时该事务在 node 的真后代上仍持锁。
// Descendant 为（按内部顺序遇到的）第一个仍持锁的真后代。
type DescendantLockError struct {
	Txn        int64
	Node       string
	Descendant string
	Mode       Mode
}

func (e *DescendantLockError) Error() string {
	return fmt.Sprintf("事务 %d 仍在真后代 %q 上持有 %q，不能释放节点 %q",
		e.Txn, e.Descendant, e.Mode, e.Node)
}

func (e *DescendantLockError) Unwrap() error { return ErrDescendantLocked }
