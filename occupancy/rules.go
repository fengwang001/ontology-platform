package occupancy

import (
	"fmt"
	"sort"
)

// checkResult 是一次规则审查的判定依据。
type checkResult struct {
	code ErrorCode
	why  string
}

func (c checkResult) fail() bool { return c.code != 0 }

func (c checkResult) err() CodeError {
	if c.code == 0 {
		return CodeError{}
	}
	return CodeError{Code: c.code, Detail: c.why}
}

// eval 是一次审查的输入：拟放入的片段候选 + 路网上下文 + 需要排除的片段键
// （延期时排除本许可自身当前占用）。
type eval struct {
	candidate piece
	exclude   map[int64]bool
}

// sameRoadCheck 同路段车道数检查：
// 相交片段封闭车道数之和（含候选）不得超过路段车道数；
// 这也自动覆盖“全封闭与任何相交许可冲突”。
func (s *Service) sameRoadCheck(e eval) checkResult {
	road := s.indices[e.candidate.road]
	iv := Interval{e.candidate.start, e.candidate.end}
	hit := road.intersecting(iv)
	total := e.candidate.lanes
	for _, p := range hit {
		if e.exclude != nil && e.exclude[p.key] {
			continue
		}
		total += p.lanes
	}
	if total > s.lanes[e.candidate.road] {
		return checkResult{ErrSameRoadConflict, fmt.Sprintf(
			"road %s [%d,%d): closed lanes %d > %d with %d intersecting permit(s)",
			e.candidate.road, e.candidate.start, e.candidate.end,
			total, s.lanes[e.candidate.road], len(hit))}
	}
	return checkResult{}
}

// detourCheck 绕行冲突检查，两个方向一次对称判定：
//  1. 候选路段全封闭时，其指定绕行路线上任何相交封闭都冲突；
//  2. 候选路段有任何封闭时，把它列入绕行路线的其他路段若存在相交的
//     全封闭，同样冲突。
//
// 只在常规许可之间判定；应急片段不进入绕行规则。
func (s *Service) detourCheck(e eval) checkResult {
	c := e.candidate
	iv := Interval{c.start, c.end}
	if c.full {
		for _, d := range s.detour[c.road] {
			for _, p := range s.indices[d].intersecting(iv) {
				if p.priority != Regular || (e.exclude != nil && e.exclude[p.key]) {
					continue
				}
				return checkResult{ErrDetourConflict, fmt.Sprintf(
					"full closure on %s [%d,%d) but detour road %s is occupied by permit %d",
					c.road, c.start, c.end, d, p.permitID)}
			}
		}
	}
	for _, up := range s.revDetour[c.road] {
		// up 是把 c.road 列入绕行路线的路段；up 上若有常规全封闭则冲突。
		for _, p := range s.indices[up].intersecting(iv) {
			if p.priority != Regular || !p.full || (e.exclude != nil && e.exclude[p.key]) {
				continue
			}
			return checkResult{ErrDetourConflict, fmt.Sprintf(
				"closure on %s [%d,%d) falls on detour of full-closed road %s permit %d",
				c.road, c.start, c.end, up, p.permitID)}
		}
	}
	return checkResult{}
}

// corridorCheck 走廊并发上限：候选时段内任一时刻处于封闭状态的
// 常规许可数不得超过走廊上限；恰等于上限允许。
func (s *Service) corridorCheck(e eval) checkResult {
	c := e.candidate
	limit, ok := s.corrCap[c.corr]
	if !ok {
		limit = 0
	}
	type ev struct {
		t    int64
		open bool
	}
	var events []ev
	events = append(events, ev{c.start, true})
	for _, p := range s.corridorPieces(c.corr) {
		if p.priority != Regular {
			continue
		}
		if e.exclude != nil && e.exclude[p.key] {
			continue
		}
		if !(Interval{p.start, p.end}).overlaps(Interval{c.start, c.end}) {
			continue
		}
		events = append(events, ev{p.start, true}, ev{p.end, false})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		// 同一时刻先闭后开：首尾相接不叠加。
		if events[i].open != events[j].open {
			return !events[i].open
		}
		return i < j
	})
	cur := 0
	peak := 0
	for _, x := range events {
		if x.open {
			cur++
			if cur > peak {
				peak = cur
			}
		} else {
			cur--
		}
	}
	if peak > limit {
		return checkResult{ErrCorridorCap, fmt.Sprintf(
			"corridor %s concurrent regular closures peak %d > cap %d over [%d,%d)",
			c.corr, peak, limit, c.start, c.end)}
	}
	return checkResult{}
}

// corridorPieces 收集一条走廊内所有路段当前活跃的片段。
// 走廊规模是静态路网规模，与该走廊历史许可总数无关（历史已归档）。
func (s *Service) corridorPieces(corridor string) []piece {
	var out []piece
	for road, idx := range s.indices {
		if s.corr[road] != corridor {
			continue
		}
		out = collectAll(idx.live, out)
	}
	return out
}

func collectAll(root *node, out []piece) []piece {
	if root == nil {
		return out
	}
	out = collectAll(root.left, out)
	out = append(out, root.p)
	out = collectAll(root.right, out)
	return out
}

// regularCheck 常规许可审查：三类冲突同时成立时只报同路段。
func (s *Service) regularCheck(e eval) checkResult {
	if c := s.sameRoadCheck(e); c.fail() {
		return c
	}
	if c := s.detourCheck(e); c.fail() {
		return c
	}
	return s.corridorCheck(e)
}

// emergencyCheck 应急受理只检查同路段车道数。
func (s *Service) emergencyCheck(e eval) checkResult {
	return s.sameRoadCheck(e)
}
