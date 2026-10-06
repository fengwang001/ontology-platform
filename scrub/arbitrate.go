package scrub

import "sort"

// Arbitrate 对单个块的当前副本集合做纯仲裁判定，不产生任何副作用。
//
// 判定次序（三类不可修复只报第一个）：
//  1. 没有任何自洽副本            -> OutcomeNoSource
//  2. 最大自洽版本 < 已提交版本    -> OutcomeCommittedLost
//  3. 最大自洽版本下摘要不一致      -> OutcomeConflict
//
// 其余情形以“最大自洽版本 + 该版本唯一摘要”为权威，位腐副本与
// 版本落后的自洽副本都需要修复；无修复目标时为 OutcomeNoRepair。
//
// 开销：仅随副本数 n 增长（排序 O(n log n)，无任何按块总数的扫描）。
func Arbitrate(replicas []Replica) Arbitration {
	n := len(replicas)
	res := Arbitration{Quorum: n/2 + 1}
	if n == 0 {
		// 无副本的块不参与巡检；交由上层判定为“块不存在”。
		res.Outcome = OutcomeNoSource
		return res
	}

	// 已提交版本：全部副本（含位腐）版本降序后的第 quorum 个。
	versions := make([]int64, n)
	for i, r := range replicas {
		versions[i] = r.Version
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	res.CommittedVersion = versions[res.Quorum-1]

	// 仲裁来源只在自洽副本中选取。
	var intact []Replica
	for _, r := range replicas {
		if r.Intact() {
			intact = append(intact, r)
		}
	}
	if len(intact) == 0 {
		res.Outcome = OutcomeNoSource
		return res
	}

	maxIntact := intact[0].Version
	for _, r := range intact[1:] {
		if r.Version > maxIntact {
			maxIntact = r.Version
		}
	}
	if maxIntact < res.CommittedVersion {
		res.Outcome = OutcomeCommittedLost
		return res
	}

	digest := ""
	for _, r := range intact {
		if r.Version != maxIntact {
			continue
		}
		if digest == "" {
			digest = r.SavedDigest
			continue
		}
		if r.SavedDigest != digest {
			res.Outcome = OutcomeConflict
			return res
		}
	}

	res.Outcome = OutcomeNoRepair
	res.AuthorityVersion = maxIntact
	res.AuthorityDigest = digest
	for _, r := range replicas {
		switch {
		case !r.Intact():
			// 位腐副本无论版本高低都按权威重写。
			res.Targets = append(res.Targets, RepairTarget{Node: r.Node, Kind: RepairBitrot})
		case r.Version < maxIntact:
			res.Targets = append(res.Targets, RepairTarget{Node: r.Node, Kind: RepairStale})
		}
	}
	if len(res.Targets) > 0 {
		res.Outcome = OutcomeRepaired
	}
	return res
}
