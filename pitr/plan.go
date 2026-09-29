package pitr

import (
	"context"
	"fmt"
)

// replayEnd maps (target, mode) to the exclusive end of the replay window:
// positions are replayed when backupEnd <= p < replayEnd.
func replayEnd(target Position, mode TargetMode) Position {
	if mode == IncludeTarget {
		return target + 1
	}
	return target
}

// chainFrontier returns the furthest archived endpoint anywhere on the
// target's ancestor chain ("已归档末端"). It only answers whether the target
// is inside the archived range; whether segments are usable at each
// position (effective timeline rule) and whether the replay window is
// contiguous is checked afterwards and reported as the first gap.
func (s *Store) chainFrontier(chain []Timeline) Position {
	frontier := chain[0].Fork
	for _, tl := range chain {
		for _, seg := range s.segments[tl.ID] {
			if seg.End > frontier {
				frontier = seg.End
			}
		}
	}
	return frontier
}

// chooseBackup returns the usable backup with the greatest contained bound
// (ties: greater timeline id), or false when no backup is usable.
func (s *Store) chooseBackup(regions []region, end Position) (Backup, bool) {
	best := Backup{}
	found := false
	for idx, r := range regions {
		for _, b := range s.backups[r.timeline] {
			if b.End < r.lo || b.End > end {
				continue
			}
			// Bounds belong to the backup's own effective interval:
			// strictly below the child fork, or exactly on it (the
			// fork-bound backup contains shared history only and is
			// also a base for the child timeline; ties then favor the
			// child timeline's id).
			if idx < len(regions)-1 && b.End > r.hi {
				continue
			}
			// A backup bound may equal the next region's fork: it
			// contains exactly the shared history and is a valid base
			// for recovering into the child timeline. Such ties are
			// resolved in favor of the greater timeline id.
			if !found || b.End > best.End || (b.End == best.End && b.Timeline > best.Timeline) {
				best, found = b, true
			}
		}
	}
	return best, found
}

// buildSteps verifies that [from, end) is fully covered, region by region,
// by the effective timeline's segments; it returns the replay steps or the
// first gap position.
func (s *Store) buildSteps(regions []region, from, end Position) ([]PlanStep, Position, bool) {
	steps := make([]PlanStep, 0)
	cursor := from
	for _, r := range regions {
		if r.hi <= cursor {
			continue
		}
		regionEnd := r.hi
		if end < regionEnd {
			regionEnd = end
		}
		if regionEnd <= cursor {
			break
		}
		merged := mergeIntervals(s.segments[r.timeline], cursor, regionEnd)
		need := cursor
		for _, step := range merged {
			if step.Start > need {
				return nil, need, false
			}
			if step.End > need {
				need = step.End
			}
			steps = append(steps, step)
		}
		if need < regionEnd {
			return nil, need, false
		}
		cursor = regionEnd
		if cursor >= end {
			break
		}
	}
	return steps, 0, true
}

// planLocked computes a plan; caller holds at least a read lock.
func (s *Store) planLocked(targetTimeline TimelineID, target Position, mode TargetMode) (Plan, error) {
	end := replayEnd(target, mode)
	chain, ok := s.ancestorChain(targetTimeline)
	if !ok {
		s.logf("输出 Plan: 拒绝 判定依据=%v (目标时间线 %d 未登记)", ErrTimelineNotFound, targetTimeline)
		return Plan{}, ErrTimelineNotFound
	}
	regions := chainRegions(chain)

	if target < 0 || end < 0 {
		s.logf("输出 Plan: 拒绝 判定依据=%v (目标 %d 非法)", ErrBeyondArchive, target)
		return Plan{}, ErrBeyondArchive
	}
	if end > s.chainFrontier(chain) {
		s.logf("输出 Plan: 拒绝 判定依据=%v (终点 %d 超出祖先链已归档末端)",
			ErrBeyondArchive, end)
		return Plan{}, ErrBeyondArchive
	}

	backup, ok := s.chooseBackup(regions, end)
	if !ok {
		s.logf("输出 Plan: 拒绝 判定依据=%v (祖先链上无 上界∈生效区间 且 上界<=%d 的备份)", ErrNoBackup, end)
		return Plan{}, ErrNoBackup
	}

	steps, gap, covered := s.buildSteps(regions, backup.End, end)
	if !covered {
		err := fmt.Errorf("%w at position %d", ErrLogGap, gap)
		s.logf("输出 Plan: 拒绝 判定依据=日志缺口 第一个缺口位置=%d (备份上界=%d 终点=%d)", gap, backup.End, end)
		return Plan{}, err
	}

	plan := Plan{
		TargetTimeline: targetTimeline,
		Target:         target,
		Mode:           mode,
		ReplayEnd:      end,
		Backup:         backup,
		Steps:          steps,
	}
	s.logf("输出 Plan: 成功 备份=timeline %d 上界 %d; 回放 %d 步 至 %d (含目标=%v); 判定依据=生效链区间全覆盖",
		backup.Timeline, backup.End, len(steps), end, mode == IncludeTarget)
	return plan, nil
}

// Plan selects a base backup and a replay plan for the target position on
// the target timeline. Error precedence:
// ErrTimelineNotFound > ErrBeyondArchive > ErrNoBackup > gap(ErrLogGap).
func (s *Store) Plan(ctx context.Context, targetTimeline TimelineID, target Position, mode TargetMode) (Plan, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.logf("输入 Plan: 目标时间线=%d 目标位置=%d 模式=%d(0=不含目标,1=含目标)", targetTimeline, target, mode)
	return s.planLocked(targetTimeline, target, mode)
}
