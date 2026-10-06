package review

import (
	"sort"
	"testing"
)

// lexicographicallyMinPanel 以朴素枚举（组合全列）独立求字典序最小评委组，
// 仅测试使用，用于和贪心实现交叉验证。
// 调用时不得有该申报人的在途评审占用（与 CreateReview 同一语义环境）。
func lexicographicallyMinPanel(t *testing.T, s *Service, applicantID, n int, minGroups map[string]int) []int {
	t.Helper()
	s.mu.Lock()
	app := s.applicants[applicantID]
	var cand []int
	for id, rev := range s.reviewers {
		if _, busy := s.occupied[id]; busy {
			continue
		}
		if len(s.recusalSources(app, rev)) == 0 {
			cand = append(cand, id)
		}
	}
	s.mu.Unlock()
	sort.Ints(cand)

	var best []int
	var comb func(start int, picked []int)
	groupsOf := func(ids []int) map[string]int {
		m := map[string]int{}
		for _, id := range ids {
			s.mu.Lock()
			m[s.reviewers[id].Group]++
			s.mu.Unlock()
		}
		return m
	}
	comb = func(start int, picked []int) {
		if len(picked) == n {
			g := groupsOf(picked)
			for grp, need := range minGroups {
				if g[grp] < need {
					return
				}
			}
			cp := append([]int(nil), picked...)
			if best == nil || lessInts(cp, best) {
				best = cp
			}
			return
		}
		for i := start; i < len(cand); i++ {
			comb(i+1, append(picked, cand[i]))
		}
	}
	comb(0, nil)
	if best == nil {
		t.Fatalf("暴力模型也找不到可行评委组 n=%d", n)
	}
	return best
}

func lessInts(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
