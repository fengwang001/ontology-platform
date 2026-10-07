package review

// mustRecuse 判定评委对申报人是否须回避（四种来源任一）。
// 全部经由 O(1) 索引查找，开销不随历史评审总数增长；
// 每次判定递增 recusalChecks 计数器，供外部验证。
func (s *Service) mustRecuse(app *Applicant, expertID int64) bool {
	s.recusalChecks++
	e := s.experts[expertID]
	return e.Unit == app.Unit ||
		app.Relations[expertID] ||
		app.RecusalAccepted[expertID] ||
		app.VoidedExperts[expertID]
}

func satisfies(counts map[string]int, mins map[string]int) bool {
	for g, m := range mins {
		if counts[g] < m {
			return false
		}
	}
	return true
}

// draw 抽取评委组：在全部满足条件的集合中，返回按编号升序排列后
// 字典序最小者。贪心：按编号升序逐个考虑，只要纳入后仍存在可行补全
// 即纳入（交换论证可证该结果即字典序最小）。复杂度 O(E·G)，
// 与历史评审总数无关。
func (s *Service) draw(app *Applicant, n int, mins map[string]int) ([]int64, bool) {
	var cand []int64
	for _, id := range s.expertOrder {
		if !s.mustRecuse(app, id) {
			cand = append(cand, id)
		}
	}
	avail := map[string]int{}
	for _, id := range cand {
		avail[s.experts[id].Group]++
	}
	selected := make([]int64, 0, n)
	selCounts := map[string]int{}
	for _, id := range cand {
		if len(selected) == n {
			break
		}
		g := s.experts[id].Group
		avail[g]--
		rem := n - len(selected) - 1
		ok := true
		sumNeed := 0
		for mg, m := range mins {
			need := m - selCounts[mg]
			if mg == g {
				need--
			}
			if need < 0 {
				need = 0
			}
			sumNeed += need
			if need > avail[mg] {
				ok = false
				break
			}
		}
		if ok && sumNeed <= rem {
			selected = append(selected, id)
			selCounts[g]++
		}
	}
	if len(selected) < n {
		return nil, false
	}
	return selected, true
}

// findSubstitute 在保持其余评委不变的前提下，选出编号最小且使整组
// 仍满足专业组要求的替补者。
func (s *Service) findSubstitute(app *Applicant, curSet map[int64]bool, counts map[string]int, mins map[string]int) (int64, bool) {
	for _, id := range s.expertOrder {
		if curSet[id] || s.mustRecuse(app, id) {
			continue
		}
		g := s.experts[id].Group
		counts[g]++
		ok := satisfies(counts, mins)
		counts[g]--
		if ok {
			return id, true
		}
	}
	return 0, false
}
