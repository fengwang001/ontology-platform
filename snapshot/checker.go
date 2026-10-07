package snapshot

import "fmt"

// Checker 负责原子性与引用完整性的跨写入核对。它按日志顺序
// 推进，维护“当前已可见对象”的集合，并对每条增量记录（一笔
// 完整事务）依次执行固定顺序的检查：先原子性（R4），后引用
// 完整性（R5）。
type Checker struct {
	visibleObjects map[string]bool
	closedTxns     map[TxnID]bool // 已完整输出过的事务
}

// NewChecker 构造核对器。
func NewChecker() *Checker {
	return &Checker{
		visibleObjects: map[string]bool{},
		closedTxns:     map[TxnID]bool{},
	}
}

// CheckSnapshot 核对快照整体状态的引用完整性：快照中任何一条
// 链接的两端对象必须也在快照中。核对通过后，快照中的对象计入
// 可见对象集合，作为后续增量核对的起点。
func (c *Checker) CheckSnapshot(state State) *ExportError {
	for id, l := range state.Links {
		if _, ok := state.Objects[l.Src]; !ok {
			return &ExportError{
				Kind:   ErrReferentialConflict,
				Rule:   RuleLinkEndpointsFirst,
				Detail: fmt.Sprintf("link %s src object %s not visible in snapshot", id, l.Src),
			}
		}
		if _, ok := state.Objects[l.Dst]; !ok {
			return &ExportError{
				Kind:   ErrReferentialConflict,
				Rule:   RuleLinkEndpointsFirst,
				Detail: fmt.Sprintf("link %s dst object %s not visible in snapshot", id, l.Dst),
			}
		}
	}
	for id := range state.Objects {
		c.visibleObjects[id] = true
	}
	return nil
}

// CheckIncrement 核对一条增量记录。原子性检查（R4）：该事务此前
// 不得已有任何写入被输出（否则说明同事务写入被隔开，无法聚合）。
// 引用完整性检查（R5）：事务新增链接的两端对象必须已可见，或在
// 本事务内新建。核对通过后更新可见对象集合。
func (c *Checker) CheckIncrement(incr Increment) *ExportError {
	if c.closedTxns[incr.Txn] {
		return &ExportError{
			Kind:   ErrAtomicityConflict,
			Rule:   RuleTxnContiguous,
			Detail: fmt.Sprintf("transaction %s writes are not contiguous in the journal", incr.Txn),
			LSN:    incr.FromLSN,
		}
	}
	created := map[string]bool{}
	for _, w := range incr.Writes {
		if w.Kind == KindObjectUpsert {
			created[w.Object.ID] = true
		}
	}
	for _, w := range incr.Writes {
		if w.Kind != KindLinkUpsert {
			continue
		}
		for _, ep := range [2]string{w.Link.Src, w.Link.Dst} {
			if !c.visibleObjects[ep] && !created[ep] {
				return &ExportError{
					Kind:   ErrReferentialConflict,
					Rule:   RuleLinkEndpointsFirst,
					Detail: fmt.Sprintf("link %s endpoint object %s not visible", w.Link.ID, ep),
					LSN:    w.LSN,
				}
			}
		}
	}
	for id := range created {
		c.visibleObjects[id] = true
	}
	c.closedTxns[incr.Txn] = true
	return nil
}
