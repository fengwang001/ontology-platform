package cardinality

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrLinkNotFound 待处置或查询的链接不存在。
	ErrLinkNotFound = errors.New("cardinality: link not found")
	// ErrNotPending 链接不处于待处理状态，无法 Finalize。
	ErrNotPending = errors.New("cardinality: link is not pending")
	// ErrDuplicateLink 重复登记同一链接 ID。
	ErrDuplicateLink = errors.New("cardinality: duplicate link id")
	// ErrDuplicateDerived 重复注册同一派生状态 ID。
	ErrDuplicateDerived = errors.New("cardinality: duplicate derived state id")
)

// Engine 是基数级联处理机制的并发安全入口。
// 所有变更操作在同一把串行锁下取得全局全序序号，
// 因而并发交织的结果等价于按该全序串行执行。
type Engine struct {
	mu    sync.Mutex
	store *store
}

// NewEngine 创建空引擎。objectValidFn 用于判定对象当前是否仍有效
// （未被撤销）；为 nil 时所有对象恒有效。
func NewEngine(objectValidFn func(objectID string) bool) *Engine {
	return &Engine{store: newStore(objectValidFn)}
}

// SetLimit 调整某方向基数上限并执行确定性对账：
// 容量之外的既有链接按 (seq, id) 全序标记为超额（升序标记），
// 容量重新容纳得下的待处理链接按标记逆序恢复。
func (e *Engine) SetLimit(key BucketKey, newLimit int) SetLimitResult {
	if newLimit < 0 {
		newLimit = 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	b := s.ensureBucket(key)
	oldLimit := b.baseLimit
	b.baseLimit = newLimit
	seq := s.nextSeq()

	marked, restored := s.reconcile(b)
	s.audit(EventLimitChanged, key, "", ReasonNone, nil,
		fmt.Sprintf("limit %d -> %d", oldLimit, newLimit),
		fmt.Sprintf("marked=%d restored=%d", len(marked), len(restored)))

	res := SetLimitResult{OrderSeq: seq}
	for _, lk := range marked {
		res.Marked = append(res.Marked, lk.ID)
	}
	for _, lk := range restored {
		res.Restored = append(res.Restored, lk.ID)
	}
	return res
}

// CreateLink 在全序中登记一条新链接。若当前有效链接数已达该方向
// 当前有效上限，则按新上限拒绝并返回原因码。
func (e *Engine) CreateLink(linkType, sourceID, targetID, linkID string) CreateResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	if _, dup := s.links[linkID]; dup {
		return CreateResult{RejectReason: Reason("duplicate_link_id")}
	}

	key := BucketKey{LinkType: linkType, Direction: Outgoing, SourceID: sourceID}
	b := s.ensureBucket(key)
	ordered := s.orderedLinks(b)
	capacity := s.effectiveCapacity(b, ordered)
	activeCount := 0
	for _, lk := range ordered {
		if lk.State == StateActive {
			activeCount++
		}
	}

	// 取号即确定全序位置：拒绝判定使用的上限就是该时刻已生效的上限，
	// 不可能出现"按旧上限接受、下调却声称先生效"的矛盾。
	seq := s.nextSeq()
	if activeCount >= capacity {
		s.audit(EventCreateDenied, key, linkID, ReasonCreateRejected, nil,
			"rejected", fmt.Sprintf("active=%d capacity=%d", activeCount, capacity))
		return CreateResult{Accepted: false, RejectReason: ReasonCreateRejected, OrderSeq: seq}
	}

	lk := &Link{
		ID:       linkID,
		LinkType: linkType,
		SourceID: sourceID,
		TargetID: targetID,
		State:    StateActive,
		Seq:      seq,
	}
	s.links[linkID] = lk
	b.members[linkID] = true
	s.audit(EventLinkCreated, key, linkID, ReasonNone, nil, string(StateActive),
		fmt.Sprintf("active=%d capacity=%d", activeCount+1, capacity))
	return CreateResult{Accepted: true, Link: lk, OrderSeq: seq}
}

// Finalize 对一条待处理链接执行显式后续处理。
// disposition 为保留或删除；对象已撤销时删除判定优先。
func (e *Engine) Finalize(linkID string, disposition Disposition) (FinalizeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	lk, ok := s.links[linkID]
	if !ok {
		return FinalizeResult{}, ErrLinkNotFound
	}
	if lk.State != StatePending {
		return FinalizeResult{}, ErrNotPending
	}
	key := BucketKey{LinkType: lk.LinkType, Direction: Outgoing, SourceID: lk.SourceID}
	b := s.ensureBucket(key)
	seq := s.nextSeq()

	// 优先判定：所依赖对象已被撤销时，无论请求意图为何都只能删除。
	if !s.objectValid(lk.TargetID) {
		s.deletePending(b, lk)
		s.audit(EventFinalized, key, lk.ID, ReasonObjectRevoked, cloneBasis(lk.MarkBasis),
			string(DispositionDelete), "target object revoked; retain not possible, forced delete")
		return FinalizeResult{
			LinkID: linkID, Kept: false, Applied: DispositionDelete,
			Reason: ReasonObjectRevoked, OrderSeq: seq,
		}, nil
	}

	switch disposition {
	case DispositionDelete:
		s.deletePending(b, lk)
		s.audit(EventFinalized, key, lk.ID, ReasonFinalDelete, nil,
			string(DispositionDelete), "explicit final delete")
		return FinalizeResult{
			LinkID: linkID, Kept: false, Applied: DispositionDelete,
			Reason: ReasonFinalDelete, OrderSeq: seq,
		}, nil
	case DispositionRetain:
		// 保留 = 相应提升该方向的有效上限：将链接加入保留集合，
		// 使有效容量必须容纳到它的全序名次，随后由确定性对账恢复。
		basis := lk.MarkBasis
		b.retained[linkID] = true
		s.reconcile(b)
		if lk.State != StateActive {
			// 理论上不会发生：保留集合保证其名次落在容量之内。
			return FinalizeResult{}, errors.New("cardinality: retain failed after capacity raise")
		}
		s.audit(EventFinalized, key, lk.ID, ReasonFinalRetain, cloneBasisIfSet(basis),
			string(DispositionRetain), "retained; effective limit raised to cover its rank")
		return FinalizeResult{
			LinkID: linkID, Kept: true, Applied: DispositionRetain,
			Reason: ReasonFinalRetain, OrderSeq: seq,
		}, nil
	default:
		return FinalizeResult{}, fmt.Errorf("cardinality: unknown disposition %q", disposition)
	}
}

// RevokeObject 撤销对象：该对象作为目标的待处理链接将只能转为删除。
func (e *Engine) RevokeObject(objectID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	if s.revoked[objectID] {
		return 0
	}
	s.revoked[objectID] = true
	s.seq++
	affected := 0
	for _, lk := range s.links {
		if lk.TargetID == objectID && lk.State == StatePending {
			affected++
		}
	}
	s.audit(EventObjectRevoked, BucketKey{}, objectID, ReasonObjectRevoked, nil,
		"revoked", fmt.Sprintf("pending links forced to delete path: %d", affected))
	return affected
}

// RegisterDerived 为某条链接注册依赖其存在性的派生状态。
func (e *Engine) RegisterDerived(derivedID, linkID, payload string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	if _, dup := s.derived[derivedID]; dup {
		return ErrDuplicateDerived
	}
	lk, ok := s.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	d := &DerivedState{
		ID:      derivedID,
		LinkID:  linkID,
		Payload: payload,
		Suspect: lk.State == StatePending,
		Cleared: lk.State == StateDeleted,
	}
	s.derived[derivedID] = d
	return nil
}

// QueryDerived 查询派生状态；链接待处理时必须明确标注为不可信。
func (e *Engine) QueryDerived(derivedID string) (DerivedView, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.store
	d, ok := s.derived[derivedID]
	if !ok {
		return DerivedView{}, false
	}
	lk := s.links[d.LinkID]
	view := DerivedView{State: *d}
	switch {
	case d.Cleared:
		view.Fresh = false
		view.Notice = "derived state has been cleared: dependency link was finally deleted"
	case d.Suspect || (lk != nil && lk.State == StatePending):
		view.Fresh = false
		view.Notice = "derived state is temporarily suspect: dependency link is pending (over-limit), result basis is stale"
	default:
		view.Fresh = true
	}
	if lk != nil && lk.State == StatePending {
		view.State.Suspect = true
	}
	return view, true
}

// deletePending 将一条待处理链接转为最终物理删除，
// 同步清理其派生状态，并让同桶后续链接确定性地补位。
func (s *store) deletePending(b *bucket, lk *Link) {
	lk.State = StateDeleted
	lk.MarkedAtSeq = 0
	lk.MarkBasis = MarkBasis{}
	delete(b.members, lk.ID)
	delete(b.retained, lk.ID)
	b.pendingCount--
	s.clearDerived(lk)
	// 删除空出容量后，其余仍 pending 的链接按统一确定性规则补位。
	s.reconcile(b)
}
