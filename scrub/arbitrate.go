package scrub

import "sort"

// Verdict 是一次仲裁的结论（纯函数输出，不依赖服务状态）。
type Verdict struct {
	Outcome          Outcome
	CommittedVersion uint64
	AuthVersion      uint64
	AuthDigest       string
	// RepairTargets 需要修复的节点（升序）：全部位腐副本
	// 以及版本低于权威版本的自洽副本。
	RepairTargets []uint64
}

// Quorum 返回 n 个副本的法定数：n/2 向下取整再加一。
func Quorum(n int) int {
	return n/2 + 1
}

// Arbitrate 对一个块的副本快照做仲裁，开销只随副本数增长。
// 调用方保证 replicas 至少有一个元素。
//
// 判定次序（只报最先命中的一类）：
//  1. 没有任何自洽副本 -> OutcomeNoSource；
//  2. 自洽副本最大版本低于已提交版本 -> OutcomeCommittedDataLost；
//  3. 最大版本下的自洽副本保存摘要不一致 -> OutcomeVersionConflict；
//  4. 否则最大自洽版本的摘要即权威：全部位腐副本与版本低于
//     权威版本的自洽副本进入修复目标；无修复目标则为 OutcomeConsistent。
//
// 已提交版本 = 全部副本（含位腐）版本号降序的第 Quorum(n) 个；
// 高于已提交版本的未提交写入只要自洽且不冲突，同样按权威处理（向前提交）。
func Arbitrate(replicas []Replica) Verdict {
	if len(replicas) == 0 {
		panic("scrub: Arbitrate requires at least one replica")
	}

	versions := make([]uint64, len(replicas))
	for i, r := range replicas {
		versions[i] = r.Version
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	committed := versions[Quorum(len(versions))-1]

	v := Verdict{CommittedVersion: committed}

	var maxSelf uint64
	hasSelf := false
	for _, r := range replicas {
		if r.SelfConsistent() && (!hasSelf || r.Version > maxSelf) {
			maxSelf = r.Version
			hasSelf = true
		}
	}
	if !hasSelf {
		v.Outcome = OutcomeNoSource
		return v
	}
	v.AuthVersion = maxSelf

	if maxSelf < committed {
		v.Outcome = OutcomeCommittedDataLost
		return v
	}

	digest := ""
	for _, r := range replicas {
		if !r.SelfConsistent() || r.Version != maxSelf {
			continue
		}
		if digest == "" {
			digest = r.Stored
		} else if r.Stored != digest {
			v.Outcome = OutcomeVersionConflict
			return v
		}
	}
	v.AuthDigest = digest

	for _, r := range replicas {
		if !r.SelfConsistent() || r.Version < maxSelf {
			v.RepairTargets = append(v.RepairTargets, r.NodeID)
		}
	}
	sort.Slice(v.RepairTargets, func(i, j int) bool { return v.RepairTargets[i] < v.RepairTargets[j] })

	if len(v.RepairTargets) == 0 {
		v.Outcome = OutcomeConsistent
	} else {
		v.Outcome = OutcomeRepaired
	}
	return v
}
