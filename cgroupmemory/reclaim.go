package cgroupmemory

import "sort"

type reclaimCandidate struct {
	group *Group
	over  int64
}

func (c *Calculator) reclaim(need int64) ReclaimResult {
	values := c.effectiveMap()
	leaves := c.collectLeaves()

	result := ReclaimResult{Events: make([]ReclaimEvent, 0)}
	remaining := need

	candidates := make([]reclaimCandidate, 0, len(leaves))
	for _, leaf := range leaves {
		over := leaf.usage - values[leaf].low
		if over > 0 {
			candidates = append(candidates, reclaimCandidate{group: leaf, over: over})
		}
	}
	sortReclaimCandidates(candidates)

	for _, candidate := range candidates {
		if remaining == 0 {
			break
		}
		bytes := minInt64(remaining, candidate.over)
		candidate.group.usage -= bytes
		remaining -= bytes
		result.Reclaimed += bytes
		result.Events = append(result.Events, ReclaimEvent{
			Path:  candidate.group.path,
			Bytes: bytes,
			Pass:  1,
		})
	}

	if remaining > 0 {
		candidates = candidates[:0]
		for _, leaf := range leaves {
			over := leaf.usage - values[leaf].min
			if over > 0 {
				candidates = append(candidates, reclaimCandidate{group: leaf, over: over})
			}
		}
		sortReclaimCandidates(candidates)

		for _, candidate := range candidates {
			if remaining == 0 {
				break
			}
			bytes := minInt64(remaining, candidate.over)
			candidate.group.usage -= bytes
			remaining -= bytes
			result.Reclaimed += bytes
			result.Events = append(result.Events, ReclaimEvent{
				Path:  candidate.group.path,
				Bytes: bytes,
				Pass:  2,
			})
		}
	}

	result.Insufficient = remaining > 0
	return result
}

func (c *Calculator) collectLeaves() []*Group {
	leaves := make([]*Group, 0)
	var walk func(group *Group)
	walk = func(group *Group) {
		if len(group.children) == 0 {
			leaves = append(leaves, group)
			return
		}
		for _, child := range c.childrenInPathOrder(group) {
			walk(child)
		}
	}
	walk(c.root)
	return leaves
}

func sortReclaimCandidates(candidates []reclaimCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].over != candidates[j].over {
			return candidates[i].over > candidates[j].over
		}
		return candidates[i].group.path < candidates[j].group.path
	})
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
