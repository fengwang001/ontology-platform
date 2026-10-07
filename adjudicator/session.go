package adjudicator

import "slices"

// Session 表示一次重建执行过程。它按裁决给出的顺序逐条推进，
// 并处理重建中途新发现的损坏：立即停止依赖该损坏的后续重建，
// 已完成的部分不撤销，所有依赖关系重新评估。
type Session struct {
	adj      Adjudicator
	snapshot *Snapshot
	// initial 为开启会话时的静态裁决结果。
	initial Verdict
	// pending 为尚未下发、按唯一确定顺序排列的待重建记录。
	pending []RecordRef
	// outstanding 为已下发但未标记完成的记录。
	outstanding map[RecordRef]bool
	// done 为已完成重建的记录，按完成顺序排列，绝不撤销。
	done    []RecordRef
	doneSet map[RecordRef]bool
	// blocked 为因中途新发现损坏而被停止重建的记录。
	blocked []RecordVerdict
	// blockedSet 用于阻断列表去重（多次报告损坏时）。
	blockedSet map[RecordRef]bool
	// late 为中途新发现损坏的记录。
	late []RecordRef
}

// NewSession 基于快照的裁决结果开启一次重建会话。
func NewSession(s *Snapshot) *Session {
	adj := New()
	v := adj.Adjudicate(s)
	return &Session{
		adj:         adj,
		snapshot:    s,
		initial:     v,
		pending:     slices.Clone(v.Order),
		outstanding: map[RecordRef]bool{},
		doneSet:     map[RecordRef]bool{},
		blockedSet:  map[RecordRef]bool{},
	}
}

// Verdict 返回开启会话时的静态裁决结果。
func (s *Session) Verdict() Verdict {
	return s.initial
}

// Next 返回下一条应重建的记录；ok 为 false 表示没有可继续推进的记录。
// 被中途损坏阻断的记录不会从 Next 返回，可通过 Blocked 查询。
func (s *Session) Next() (ref RecordRef, ok bool) {
	if len(s.pending) == 0 {
		return RecordRef{}, false
	}
	ref = s.pending[0]
	s.pending = s.pending[1:]
	s.outstanding[ref] = true
	return ref, true
}

// Complete 标记一条记录已重建完成。已完成的记录不会因后续
// 新发现的损坏而被撤销。
func (s *Session) Complete(ref RecordRef) {
	if s.doneSet[ref] {
		return
	}
	delete(s.outstanding, ref)
	s.doneSet[ref] = true
	s.done = append(s.done, ref)
}

// ReportCorruption 报告重建中途新发现的损坏。会话立即重新评估
// 所有未完成记录的依赖关系，依赖该损坏记录的后续重建被停止。
// 原始快照不被修改：重新评估在带新损坏标记的快照副本上进行。
func (s *Session) ReportCorruption(ref RecordRef) {
	s.late = append(s.late, ref)

	cp := *s.snapshot
	backup := cp.Backups[int(ref.Category)]
	damaged := make(map[string]bool, len(backup.Damaged)+1)
	for id, d := range backup.Damaged {
		damaged[id] = d
	}
	damaged[ref.ID] = true
	backup.Damaged = damaged
	cp.Backups[int(ref.Category)] = backup

	v := s.adj.Adjudicate(&cp)

	// 已完成的记录不撤销；其余记录按新的裁决结果重新划分。
	var pending []RecordRef
	for _, r := range v.Order {
		if !s.doneSet[r] {
			pending = append(pending, r)
		}
	}
	s.pending = pending
	for r := range s.outstanding {
		if !v.Rebuildable(r) {
			delete(s.outstanding, r)
		}
	}
	for _, rv := range v.Verdicts {
		if rv.Rebuildable || s.doneSet[rv.Ref] {
			continue
		}
		if s.blockedSet[rv.Ref] {
			continue
		}
		if !s.initial.Rebuildable(rv.Ref) && rv.Ref != ref {
			// 静态裁决本就不可重建的记录不属于"中途被停止"。
			continue
		}
		s.blockedSet[rv.Ref] = true
		s.blocked = append(s.blocked, RecordVerdict{
			Ref:         rv.Ref,
			Rebuildable: false,
			Reason:      ReasonLateCorruption,
			BlockedBy:   rv.BlockedBy,
		})
	}
}

// Blocked 返回因中途损坏而被停止重建的记录及其判定依据。
func (s *Session) Blocked() []RecordVerdict {
	return s.blocked
}

// Done 返回已完成重建的记录，顺序与完成顺序一致。
func (s *Session) Done() []RecordRef {
	return s.done
}
