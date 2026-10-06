package review

import "sort"

// suffixCount 描述候选切片某个后缀的分组计数。
// groups[g] = 后缀中专业组 g 的人数；total 为后缀总人数。
type suffixCount struct {
	total  int
	groups map[string]int
}

// validateDrawParams 校验抽取参数，调用时持锁。
func validateDrawParams(n int, minGroups map[string]int) error {
	if n <= 0 || n%2 == 0 {
		return errInvalid("评委人数必须为正奇数: %d", n)
	}
	sum := 0
	for g, m := range minGroups {
		if g == "" {
			return errInvalid("专业组名称不可为空")
		}
		if m < 0 {
			return errInvalid("专业组最低人数不可为负: %s=%d", g, m)
		}
		sum += m
	}
	if sum > n {
		return errInvalid("各专业组最低人数之和 %d 超过评委人数 %d", sum, n)
	}
	return nil
}

// eligibleCandidates 返回某申报人可担任评委的全部评委编号（升序）：
// 无需回避（四类来源逐一判定）且未被其他在途评审占用。
// 调用时持锁。
func (s *Service) eligibleCandidates(app *applicantState, exclude map[int]struct{}) []int {
	ids := make([]int, 0, len(s.reviewers))
	for id, rev := range s.reviewers {
		if _, bad := exclude[id]; bad {
			continue
		}
		if holder, occupied := s.occupied[id]; occupied && holder != 0 {
			continue
		}
		if len(s.recusalSources(app, rev)) > 0 {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// drawPanel 返回唯一的抽取结果：所有可行 n 人集合中，编号升序排列后
// 字典序最小者。不存在时返回 ErrInsufficientReviewers。
//
// 贪心构造：按编号升序逐个决定"取/不取"，若取当前编号后仍能补齐则取。
// 可补齐性用各组"后缀候选计数"在 O(组数) 内判定，整体复杂度
// O(K log K + K*G)（K 为评委库人数，G 为专业组数），
// 与历史评审总数完全无关——回避前科在 applicantState.autoAvoid
// 这一张增量维护的集合上 O(1) 查询，不扫描任何历史评审。
// 调用时持锁。
func (s *Service) drawPanel(app *applicantState, n int, minGroups map[string]int) ([]int, error) {
	if err := validateDrawParams(n, minGroups); err != nil {
		return nil, err
	}
	cand := s.eligibleCandidates(app, nil)

	// 后缀计数：sx[i].groups[g] = cand[i:] 中专业组 g 的人数。
	sx := make([]suffixCount, len(cand)+1)
	for i := range sx {
		sx[i].groups = map[string]int{}
	}
	for i := len(cand) - 1; i >= 0; i-- {
		sx[i].total = sx[i+1].total + 1
		for g, c := range sx[i+1].groups {
			sx[i].groups[g] = c
		}
		sx[i].groups[s.reviewers[cand[i]].Group]++
	}

	remainMin := map[string]int{}
	for g, m := range minGroups {
		remainMin[g] = m
	}

	picked := make([]int, 0, n)
	for i := 0; i < len(cand) && len(picked) < n; i++ {
		g := s.reviewers[cand[i]].Group
		// 试取 cand[i]：更新尚未满足的专业组下限。
		if remainMin[g] > 0 {
			remainMin[g]--
		}
		need := n - len(picked) - 1
		if feasible(need, remainMin, sx[i+1]) {
			picked = append(picked, cand[i])
			continue
		}
		// 不可补齐则放弃 cand[i]，回滚下限。
		if minGroups[g] > 0 {
			remainMin[g]++
		}
	}

	if len(picked) != n {
		return nil, errShort("满足回避与专业组要求的评委不足 %d 人", n)
	}
	sort.Ints(picked)
	return picked, nil
}

// feasible 判定：还需 need 人，各组至少 remainMin[g] 人，
// 仅使用后缀计数 sf 描述的候选时能否完成。调用时持锁。
func feasible(need int, remainMin map[string]int, sf suffixCount) bool {
	minSum := 0
	for _, m := range remainMin {
		minSum += m
	}
	if need < minSum || sf.total < need {
		return false
	}
	for g, m := range remainMin {
		if sf.groups[g] < m {
			return false
		}
	}
	return true
}

// pickSubstitute 在保持其余评委不变的前提下选择唯一替补：
// 可行即剩余 panel 已满足除离组外的全部下限，因此离组若缺额则
// 替补必须来自该组，否则可取任意组；在可行候选中取编号最小者。
// 调用时持锁。
func (s *Service) pickSubstitute(app *applicantState, r *reviewState, leftGroup string) (int, bool) {
	exclude := map[int]struct{}{}
	for id := range r.panel {
		exclude[id] = struct{}{}
	}
	cand := s.eligibleCandidates(app, exclude)

	remaining := map[string]int{}
	for id := range r.panel {
		remaining[s.reviewers[id].Group]++
	}
	needGroup := ""
	if remaining[leftGroup] < r.minGroups[leftGroup] {
		needGroup = leftGroup
	}
	for _, id := range cand {
		if needGroup != "" && s.reviewers[id].Group != needGroup {
			continue
		}
		return id, true
	}
	return 0, false
}
