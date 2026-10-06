package underwriting

import "sort"

// decide 按快照对一次投保（或再裁定）求值，返回结论与考察过的候选规则数。
// 合并次序：规则拒保 > 加费超限拒保 > 延期 > 需体检 > 承保。
// liftExamLimit 为 true 时（体检已通过）免体检限额视同无限制。
func decide(snap *Snapshot, age int, occupation int, disclosures map[string]bool,
	sumAssured int64, appliedAt int64, premiumCap int, examLimit int64, liftExamLimit bool) (Decision, int) {

	candidates := snap.candidates(int64(age), occupation)
	examined := len(candidates)

	totalPercent := 0
	exclusions := map[string]bool{}
	postponed := false
	var postponeUntil int64

	for _, r := range candidates {
		if appliedAt < r.Start || appliedAt >= r.End {
			continue // 生效区间左含右不含
		}
		if !disclosureHit(r.Cond.Disclosures, disclosures) {
			continue
		}
		switch r.Action.Kind {
		case ActionDecline:
			return Decision{Kind: DecRuleDecline}, examined
		case ActionExtraPremium:
			totalPercent += r.Action.Percent
		case ActionExclusion:
			exclusions[r.Action.ExclusionCode] = true
		case ActionPostpone:
			if !postponed || r.Action.PostponeUntil > postponeUntil {
				postponeUntil = r.Action.PostponeUntil
			}
			postponed = true
		case ActionStandard:
		}
	}

	if totalPercent > premiumCap {
		return Decision{Kind: DecCapExceeded}, examined
	}
	if postponed {
		return Decision{Kind: DecPostpone, PostponeUntil: postponeUntil}, examined
	}
	if !liftExamLimit && sumAssured > examLimit {
		return Decision{Kind: DecNeedExam}, examined
	}
	codes := make([]string, 0, len(exclusions))
	for c := range exclusions {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		codes = nil
	}
	return Decision{Kind: DecAccept, ExtraPremium: totalPercent, Exclusions: codes}, examined
}

// disclosureHit 报告规则的健康告知条件是否命中：规则缺省不限，或有任一交集。
func disclosureHit(ruleSet map[string]bool, disclosures map[string]bool) bool {
	if len(ruleSet) == 0 {
		return true
	}
	for code := range ruleSet {
		if disclosures[code] {
			return true
		}
	}
	return false
}
