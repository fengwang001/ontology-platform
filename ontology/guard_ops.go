package ontology

import (
	"container/heap"
	"time"
)

// reapExpiredLocked 在持锁状态下回收所有已到中断期限的进行中占用。
// 回收与随后的准入判定在同一个临界区内，因此"释放后新请求立刻可见"
// 与"释放前到达的请求必须看到占用"是同一个原子事实。
func (g *Guard) reapExpiredLocked(s *scope, now time.Time) []string {
	var expired []string
	for s.Len() > 0 {
		r := s.deadlineHeap[0]
		if r.deadline.After(now) {
			return expired
		}
		heap.Pop(&s.deadlineHeap)
		delete(s.pending, r.requestID)
		delete(g.byReq, r.requestID)
		s.outcomes[r.requestID] = OutcomeExpired
		expired = append(expired, r.requestID)
		g.journal.Append(Event{
			RequestID: r.requestID,
			Scope:     s.key,
			Kind:      EventExpire,
			Baseline:  s.version,
			Confirmed: len(s.links),
			InFlight:  len(s.pending),
			Reserve:   false,
			At:        now,
			Source:    r.source,
			Target:    r.target,
		})
	}
	return expired
}

// Begin 尝试在指定作用域新建关联。
//
// 判定顺序（原因互斥，优先级从高到低）：
//  1. 基线版本冲突（ObservedVersion != 当前版本）
//  2. 关联已存在（不重复计数）
//  3. 已确认关联数达到上限
//  4. 进行中请求占用名额（暂时性拒绝，幻影防护）
func (g *Guard) Begin(req BeginRequest) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	s, ok := g.scopes[req.Scope]
	if !ok {
		s = newScope(req.Scope, 0)
		g.scopes[req.Scope] = s
	}
	g.reapExpiredLocked(s, now)

	link := Link{SourceID: req.SourceID, TargetID: req.TargetID}
	_, exists := s.links[link]
	confirmed := len(s.links)
	inFlight := len(s.pending)

	reject := func(reason RejectReason) Decision {
		g.journal.Append(Event{
			RequestID:   req.RequestID,
			Scope:       s.key,
			Kind:        EventReject,
			Reason:      reason,
			Baseline:    s.version,
			ObservedVer: req.ObservedVersion,
			Confirmed:   confirmed,
			InFlight:    inFlight,
			Reserve:     false,
			At:          now,
			Source:      req.SourceID,
			Target:      req.TargetID,
		})
		return Decision{Admitted: false, Reason: reason, Version: s.version,
			Confirmed: confirmed, InFlight: inFlight, Seq: int64(g.journal.Len())}
	}

	if req.ObservedVersion != s.version {
		return reject(RejectBaselineConflict)
	}
	if exists {
		return reject(RejectDuplicate)
	}
	if confirmed >= s.capacity {
		return reject(RejectConfirmedFull)
	}
	if confirmed+inFlight >= s.capacity {
		return reject(RejectInFlight)
	}

	if _, busy := g.byReq[req.RequestID]; busy {
		return Decision{Reason: RejectInFlight, Version: s.version,
			Confirmed: confirmed, InFlight: inFlight}
	}

	r := &reservation{
		requestID: req.RequestID,
		source:    req.SourceID,
		target:    req.TargetID,
		deadline:  now.Add(g.leaseTTL),
		observed:  req.ObservedVersion,
	}
	s.pending[req.RequestID] = r
	heap.Push(&s.deadlineHeap, r)
	g.byReq[req.RequestID] = s
	s.outcomes[req.RequestID] = OutcomePending

	g.journal.Append(Event{
		RequestID:   req.RequestID,
		Scope:       s.key,
		Kind:        EventAdmit,
		Baseline:    s.version,
		ObservedVer: req.ObservedVersion,
		Confirmed:   confirmed,
		InFlight:    len(s.pending),
		Reserve:     true,
		At:          now,
		Source:      req.SourceID,
		Target:      req.TargetID,
	})
	return Decision{Admitted: true, Version: s.version,
		Confirmed: confirmed, InFlight: len(s.pending), Seq: int64(g.journal.Len())}
}
