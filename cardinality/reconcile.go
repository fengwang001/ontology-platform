package cardinality

import "sort"

// reconcile 依据当前基础上限与保留集合，对桶状态做一次确定性对账。
//
// 标记规则（确定性）：桶内已登记未删除链接按 (seq 升序, ID 字典序)
// 全序排列；有效容量 capacity 之外的 active 链接被标记为超额，
// 标记顺序为全序升序。同一组输入重复执行得到完全相同的超额集合。
//
// 恢复规则：容量重新容纳得下的 pending 链接恢复为有效；
// 恢复顺序与其被标记为超额时的顺序相反
// （先按标记批次序号降序，同批再按名次降序）。
//
// 已撤销对象相关的待处理链接不参与自动恢复：它们只能走显式处置的
// 强制删除路径（对象撤销判定优先）。
//
// 返回值 marked 按全序升序，restored 按标记顺序的逆序。
func (s *store) reconcile(b *bucket) (marked, restored []*Link) {
	ordered := s.orderedLinks(b)
	capacity := s.effectiveCapacity(b, ordered)

	allIDs := make([]string, len(ordered))
	for i, lk := range ordered {
		allIDs[i] = lk.ID
	}

	type rankLink struct {
		rank int
		link *Link
	}
	var toRestore []rankLink

	for i, lk := range ordered {
		withinCapacity := i < capacity
		switch {
		case withinCapacity && lk.State == StateActive:
			// 无变化。
		case !withinCapacity && lk.State == StateActive:
			basis := MarkBasis{
				LimitAtMark: b.baseLimit,
				RankAtMark:  i + 1,
				TotalAtMark: len(ordered),
				OrderedIDs:  append([]string(nil), allIDs...),
			}
			lk.State = StatePending
			lk.MarkedAtSeq = s.seq
			lk.MarkBasis = basis
			b.pendingCount++
			marked = append(marked, lk)
			s.suspendDerived(lk)
			s.audit(EventMarked, b.key, lk.ID, ReasonMarkedExcess, cloneBasis(basis),
				string(StatePending), "rank exceeds effective capacity after limit change")
		case withinCapacity && lk.State == StatePending:
			if !s.objectValid(lk.TargetID) {
				// 对象已撤销：不可自动恢复，等待显式强制删除处置。
				continue
			}
			toRestore = append(toRestore, rankLink{rank: i + 1, link: lk})
		}
	}

	// 恢复顺序 = 标记顺序的逆序：标记批次新的先恢复；
	// 同一批次内是按全序升序标记的，故按名次降序恢复。
	sort.SliceStable(toRestore, func(i, j int) bool {
		if toRestore[i].link.MarkedAtSeq != toRestore[j].link.MarkedAtSeq {
			return toRestore[i].link.MarkedAtSeq > toRestore[j].link.MarkedAtSeq
		}
		return toRestore[i].rank > toRestore[j].rank
	})
	for _, item := range toRestore {
		lk := item.link
		basis := lk.MarkBasis
		lk.State = StateActive
		lk.MarkedAtSeq = 0
		lk.MarkBasis = MarkBasis{}
		b.pendingCount--
		restored = append(restored, lk)
		s.resumeDerived(lk)
		s.audit(EventRestored, b.key, lk.ID, ReasonPendingRestored, cloneBasisIfSet(basis),
			string(StateActive), "restored in reverse order of marking after effective limit rose")
	}
	return marked, restored
}

func cloneBasisIfSet(b MarkBasis) *MarkBasis {
	if b.TotalAtMark == 0 {
		return nil
	}
	return cloneBasis(b)
}

func (s *store) suspendDerived(lk *Link) {
	for _, d := range s.derived {
		if d.LinkID == lk.ID && !d.Cleared && !d.Suspect {
			d.Suspect = true
			s.audit(EventDerivedChange, BucketKey{}, d.ID, ReasonDerivedSuspended, nil,
				"suspect", "derived state suspended while dependency link is pending")
		}
	}
}

func (s *store) resumeDerived(lk *Link) {
	for _, d := range s.derived {
		if d.LinkID == lk.ID && !d.Cleared && d.Suspect {
			d.Suspect = false
			s.audit(EventDerivedChange, BucketKey{}, d.ID, ReasonDerivedRestored, nil,
				"fresh", "derived state restored after dependency link became valid")
		}
	}
}

func (s *store) clearDerived(lk *Link) {
	for _, d := range s.derived {
		if d.LinkID == lk.ID && !d.Cleared {
			d.Suspect = false
			d.Cleared = true
			d.Payload = ""
			s.audit(EventDerivedChange, BucketKey{}, d.ID, ReasonDerivedCleared, nil,
				"cleared", "derived state cleared after dependency link was finally deleted")
		}
	}
}
