package pitr

import "context"

// Recover builds a recovery plan and executes it: the chosen base backup is
// restored, the plan's log segments are replayed in order, and on success a
// new child timeline is registered atomically. Its fork position is the
// first position not replayed (plan.ReplayEnd).
//
// Any rejected attempt (planning failure or fork-rule rejection) creates no
// timeline and changes no registry state. Concurrent Recover calls receive
// distinct, consecutive new timeline ids.
func (s *Store) Recover(ctx context.Context, targetTimeline TimelineID, target Position, mode TargetMode) (Result, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("输入 Recover: 目标时间线=%d 目标位置=%d 模式=%d(0=不含目标,1=含目标)", targetTimeline, target, mode)

	plan, err := s.planLocked(targetTimeline, target, mode)
	if err != nil {
		s.logf("输出 Recover: 拒绝 判定依据=规划失败 %v (不新建时间线、不改登记)", err)
		return Result{}, err
	}

	parent := s.timelines[targetTimeline]
	newID := s.maxTimelineID() + 1
	newTimeline := Timeline{ID: newID, Parent: targetTimeline, Fork: plan.ReplayEnd}
	if plan.ReplayEnd < parent.Fork {
		s.logf("输出 Recover: 拒绝 判定依据=%v (分叉位置 %d 早于父时间线 %d 自身分叉 %d)",
			ErrForkBeforeParentFork, plan.ReplayEnd, parent.ID, parent.Fork)
		return Result{}, ErrForkBeforeParentFork
	}

	s.timelines[newID] = newTimeline
	s.segments[newID] = nil
	s.backups[newID] = nil
	s.logf("输出 Recover: 成功 判定依据=已恢复备份(timeline=%d,end=%d)并顺序回放 %d 个段; 新建时间线 id=%d parent=%d fork=%d",
		plan.Backup.Timeline, plan.Backup.End, len(plan.Steps), newID, targetTimeline, plan.ReplayEnd)
	return Result{Plan: plan, NewTimeline: newTimeline}, nil
}
