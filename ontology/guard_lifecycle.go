package ontology

import (
	"container/heap"
	"sort"
)

// Heartbeat 续期进行中请求的中断租约；返回 false 表示请求已不在进行中。
// 调用方在最终确定前持续心跳；停止心跳且超过 TTL 是"中断"的唯一裁定依据。
func (g *Guard) Heartbeat(requestID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	s, ok := g.byReq[requestID]
	if !ok {
		return false
	}
	r := s.pending[requestID]
	r.deadline = now.Add(g.leaseTTL)
	heap.Fix(&s.deadlineHeap, r.heapIndex)
	g.journal.Append(Event{
		RequestID: requestID,
		Scope:     s.key,
		Kind:      EventHeartbeat,
		Baseline:  s.version,
		Confirmed: len(s.links),
		InFlight:  len(s.pending),
		Reserve:   true,
		At:        now,
	})
	return true
}

// Commit 将进行中请求确定为成功：名额原子转为已确认关联，版本号 +1。
// 若同一关联在进行期间已由其他请求建立，则提交失败并释放名额（不重复计数）。
func (g *Guard) Commit(requestID string) CommitResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	s, ok := g.byReq[requestID]
	if !ok {
		oc, _ := g.outcomeLocked(requestID)
		var ver int64
		for _, sc := range g.scopes {
			if _, known := sc.outcomes[requestID]; known {
				ver = sc.version
				break
			}
		}
		return CommitResult{OK: false, Outcome: oc, Version: ver}
	}
	r := s.pending[requestID]
	heap.Remove(&s.deadlineHeap, r.heapIndex)
	delete(s.pending, requestID)
	delete(g.byReq, requestID)

	link := Link{SourceID: r.source, TargetID: r.target}
	if _, exists := s.links[link]; exists {
		s.outcomes[requestID] = OutcomeRolledBack
		g.journal.Append(Event{
			RequestID: requestID,
			Scope:     s.key,
			Kind:      EventRollback,
			Reason:    RejectDuplicate,
			Baseline:  s.version,
			Confirmed: len(s.links),
			InFlight:  len(s.pending),
			Reserve:   false,
			At:        now,
			Source:    r.source,
			Target:    r.target,
		})
		return CommitResult{OK: false, Reason: RejectDuplicate,
			Outcome: OutcomeRolledBack, Version: s.version,
			Confirmed: len(s.links), InFlight: len(s.pending)}
	}

	s.links[link] = struct{}{}
	s.version++
	s.outcomes[requestID] = OutcomeCommitted
	g.journal.Append(Event{
		RequestID: requestID,
		Scope:     s.key,
		Kind:      EventCommit,
		Baseline:  s.version,
		Confirmed: len(s.links),
		InFlight:  len(s.pending),
		Reserve:   false,
		At:        now,
		Source:    r.source,
		Target:    r.target,
	})
	return CommitResult{OK: true, Version: s.version,
		Outcome: OutcomeCommitted, Confirmed: len(s.links),
		InFlight: len(s.pending)}
}

// Rollback 将进行中请求确定为失败，原子释放名额。
// 释放与下一次准入判定共用同一把锁，因此释放立即可见、不存在不确定窗口；
// 此前被拒绝的请求不会被自动唤醒重试（本设计不做排队）。
func (g *Guard) Rollback(requestID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	s, ok := g.byReq[requestID]
	if !ok {
		return false
	}
	r := s.pending[requestID]
	heap.Remove(&s.deadlineHeap, r.heapIndex)
	delete(s.pending, requestID)
	delete(g.byReq, requestID)
	s.outcomes[requestID] = OutcomeRolledBack
	g.journal.Append(Event{
		RequestID: requestID,
		Scope:     s.key,
		Kind:      EventRollback,
		Baseline:  s.version,
		Confirmed: len(s.links),
		InFlight:  len(s.pending),
		Reserve:   false,
		At:        now,
		Source:    r.source,
		Target:    r.target,
	})
	return true
}

// Reap 主动推进时钟并回收所有已过期中的进行中请求（供假时钟测试与维护调用）。
// 返回被裁定为中断的 requestID 列表；每个释放都原子且立即可见。
func (g *Guard) Reap() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	var expired []string
	for _, s := range g.scopes {
		expired = append(expired, g.reapExpiredLocked(s, now)...)
	}
	sort.Strings(expired)
	return expired
}

// Outcome 返回某请求的最终裁定；进行中为 PENDING，未知为 -1。
func (g *Guard) Outcome(requestID string) (Outcome, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.outcomeLocked(requestID)
}

func (g *Guard) outcomeLocked(requestID string) (Outcome, bool) {
	if s, ok := g.byReq[requestID]; ok {
		if _, pending := s.pending[requestID]; pending {
			return OutcomePending, true
		}
	}
	for _, s := range g.scopes {
		if oc, ok := s.outcomes[requestID]; ok {
			return oc, true
		}
	}
	return Outcome(-1), false
}

// Snapshot 是作用域可观测状态的快照（核验/演示用）。
type Snapshot struct {
	Key       ScopeKey
	Capacity  int
	Version   int64
	Confirmed int
	InFlight  int
	Links     []Link
}

// Snapshot 返回作用域当前状态；同时按当前时钟回收过期占用。
func (g *Guard) Snapshot(key ScopeKey) (Snapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()

	s, ok := g.scopes[key]
	if !ok {
		return Snapshot{}, ErrScopeNotFound
	}
	g.reapExpiredLocked(s, now)

	links := make([]Link, 0, len(s.links))
	for l := range s.links {
		links = append(links, l)
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].SourceID != links[j].SourceID {
			return links[i].SourceID < links[j].SourceID
		}
		return links[i].TargetID < links[j].TargetID
	})
	return Snapshot{
		Key:       s.key,
		Capacity:  s.capacity,
		Version:   s.version,
		Confirmed: len(s.links),
		InFlight:  len(s.pending),
		Links:     links,
	}, nil
}
