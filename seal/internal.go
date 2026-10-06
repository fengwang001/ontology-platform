package seal

import (
	"fmt"
	"sort"
	"strings"
)

// checkClock is the clock-backward boundary. Accepted operations (the very last
// step of each successful method) call advanceClock; rejected operations never
// move the clock.
func (s *Service) checkClock(now int64) *SealError {
	if now < s.lastNow {
		return sealErr(ErrClockBackward,
			fmt.Sprintf("now=%d < last accepted now=%d", now, s.lastNow))
	}
	return nil
}

func (s *Service) advanceClock(now int64) {
	if now > s.lastNow {
		s.lastNow = now
	}
}

func (s *Service) emit(now int64, op, input, output, basis string) {
	s.seq++
	s.log.Log(LogEntry{Seq: s.seq, Now: now, Op: op, Input: input, Output: output, Basis: basis})
}

func (s *Service) isCustodian(seal *Seal, person string) bool {
	return person == seal.CustodianA || person == seal.CustodianB
}

// effectiveGrant returns the deterministically chosen effective grant binding an
// employee to a seal at time t. When several overlap, the lexicographically
// smallest grant id wins, which makes replay bit-for-bit reproducible.
func (s *Service) effectiveGrant(employee, sealID string, t int64) *Grant {
	var ids []string
	for id, g := range s.grants {
		if g.Employee == employee && g.SealID == sealID && g.Effective(t) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	return s.grants[ids[0]]
}

// freezeSupport explains why the freeze check is O(1) in history size:
//
//   overruns[employee] is a min-heap containing one deadline entry per executed
//   application whose receipt has not yet been registered. Entries are pushed
//   exactly once (at execution) and removed exactly once (at receipt). The
//   minimum deadline sits at index 0, so "is any executed-but-unreceipted
//   application past its deadline at time t?" is one comparison:
//
//	len(heap) > 0 && t > heap[0]
//
//   The whole execution history is never scanned. Daily counting uses the same
//   principle: daily[grantID][dayIndex] is a single integer incremented on each
//   success, so the quota check is one map lookup plus one comparison.

func (s *Service) heapPush(employee string, deadline int64) {
	h := s.overruns[employee]
	h = append(h, deadline)
	up := func(i int) {
		for i > 0 {
			parent := (i - 1) / 2
			if h[parent] <= h[i] {
				break
			}
			h[parent], h[i] = h[i], h[parent]
			i = parent
		}
	}
	up(len(h) - 1)
	s.overruns[employee] = h
}

// heapRemoveOne removes one entry equal to deadline. Receipt order is unrelated
// to deadline order, so it removes the matching slot and re-heapifies.
func (s *Service) heapRemoveOne(employee string, deadline int64) bool {
	h := s.overruns[employee]
	idx := -1
	for i, d := range h {
		if d == deadline {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	n := len(h) - 1
	h[idx] = h[n]
	h = h[:n]
	if n > 0 && idx < n {
		down := func(i int) {
			for {
				left, right, best := 2*i+1, 2*i+2, i
				if left < n && h[left] < h[best] {
					best = left
				}
				if right < n && h[right] < h[best] {
					best = right
				}
				if best == i {
					return
				}
				h[i], h[best] = h[best], h[i]
				i = best
			}
		}
		down(idx)
		up := func(i int) {
			for i > 0 {
				parent := (i - 1) / 2
				if h[parent] <= h[i] {
					break
				}
				h[parent], h[i] = h[i], h[parent]
				i = parent
			}
		}
		up(idx)
	}
	s.overruns[employee] = h
	return true
}

// frozenAt reports whether employee is frozen at time t and, if so, the earliest
// un-receipted deadline that causes it. O(1): heap root comparison.
func (s *Service) frozenAt(employee string, t int64) (bool, int64) {
	h := s.overruns[employee]
	if len(h) > 0 && t > h[0] {
		return true, h[0]
	}
	return false, 0
}

// unfreezeLocked clears the sticky frozen flag when no overrun remains.
func (s *Service) unfreezeLocked(employee string) {
	if len(s.overruns[employee]) == 0 {
		s.frozen[employee] = false
	}
}

func dayIndex(t int64) int64 {
	// floor division: t is expected non-negative in the domain, and Go / truncates
	// toward zero; negative times are rejected as invalid parameters anyway.
	return t / SecondsPerDay
}

func joinMaterials(m map[string]struct{}) string {
	items := make([]string, 0, len(m))
	for k := range m {
		items = append(items, k)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}
