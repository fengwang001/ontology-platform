package apportion

import "sort"

// shareRecord 是一张保单在单次分摊中的裁定结果。
type shareRecord struct {
	policyNo string
	il       int64
	weight   int64 // 本阶段采用的比例权重
	pay      int64 // 本阶段应赔
}

// stageResult 记录一个阶段的裁定依据，供日志与文档对照。
type stageResult struct {
	name     string // "限额比例" 或 "独立责任比例"
	policies []shareRecord
	target   int64
	paid     int64
}

// decision 是一次受理的完整分摊裁定。
type decision struct {
	stage1   *stageResult
	stage2   *stageResult // 无超额阶段或无需进入时为 nil
	noPolicy bool
}

func (d *decision) allStages() []*stageResult {
	if d.stage2 != nil {
		return []*stageResult{d.stage1, d.stage2}
	}
	if d.stage1 != nil {
		return []*stageResult{d.stage1}
	}
	return nil
}

// settleStage 在同一阶段内按权重比例把 target 分掉，每张不超过其独立责任额。
// 算法与规模无关历史：仅依赖本次参与者快照，O(k^2)（k 为本次参与保单数）。
//
// 规则：
//  1. 按 weight/total 比例向下取整；
//  2. 按比例应得超过独立责任额（或 IL 为 0）的保单先达顶，挤出部分
//     在未达顶保单间按同比例继续分配；
//  3. 取整尾差逐分给「IL 最大且尚未达顶」者，并列给保单编号字典序最小者；
//  4. 全体达顶而仍有剩余时，剩余交还上层（无保单可再赔）。
func settleStage(name string, parts []*participant, weights []int64, target int64) *stageResult {
	res := &stageResult{name: name, target: target}
	for i, p := range parts {
		res.policies = append(res.policies, shareRecord{
			policyNo: p.policy.PolicyNo, il: p.il, weight: weights[i],
		})
	}

	pay := make([]int64, len(parts))
	capped := make([]bool, len(parts))
	remaining := target

	for remaining > 0 {
		var total int64
		for i := range parts {
			if !capped[i] && pay[i] < parts[i].il {
				total += weights[i]
			}
		}
		if total == 0 {
			break // 全体权重为 0 或已全部达顶，无法继续
		}

		floor := make([]int64, len(parts))
		var floorSum int64
		newly := -1
		for i := range parts {
			if capped[i] || pay[i] >= parts[i].il || weights[i] == 0 {
				continue
			}
			sh := remaining * weights[i] / total
			floor[i] = sh
			floorSum += sh
			headroom := parts[i].il - pay[i]
			if sh > headroom {
				// 最小保单编号优先，保证并列确定性
				if newly == -1 || parts[i].policy.PolicyNo < parts[newly].policy.PolicyNo {
					newly = i
				}
			}
		}

		if newly >= 0 {
			// 有人按比例取整后仍超顶：先把它钉在独立责任额，挤出的部分重开一轮。
			remaining -= parts[newly].il - pay[newly]
			pay[newly] = parts[newly].il
			capped[newly] = true
			continue
		}

		// 无人超顶：接受全部取整份额，尾差逐分裁定。
		for i := range floor {
			pay[i] += floor[i]
		}
		remaining -= floorSum
		for remaining > 0 {
			best := -1
			for i := range parts {
				if capped[i] || pay[i] >= parts[i].il {
					continue
				}
				if best == -1 || parts[i].il > parts[best].il ||
					(parts[i].il == parts[best].il &&
						parts[i].policy.PolicyNo < parts[best].policy.PolicyNo) {
					best = i
				}
			}
			if best == -1 {
				break
			}
			pay[best]++
			remaining--
		}
		break
	}

	for i := range res.policies {
		res.policies[i].pay = pay[i]
		res.paid += pay[i]
	}
	return res
}

// settle 执行两阶段分摊，返回完整裁定。入参为已按保单编号排序的参与者快照。
func settle(parts []*participant, amount int64) *decision {
	d := &decision{}

	var nonExcess, excess []*participant
	for _, p := range parts {
		if p.policy.Clause == Excess {
			excess = append(excess, p)
		} else {
			nonExcess = append(nonExcess, p)
		}
	}

	if len(nonExcess) == 0 && len(excess) == 0 {
		d.noPolicy = true
		return d
	}

	// 第一阶段：存在任一独立责任型即整体按独立责任额；否则按限额权重。
	useIL := false
	for _, p := range nonExcess {
		if p.policy.Clause == IndependentShare {
			useIL = true
			break
		}
	}

	primary := nonExcess
	stageName := "限额比例"
	if useIL {
		stageName = "独立责任比例"
	} else if len(nonExcess) == 0 && len(excess) > 0 {
		// 没有任何非超额保单时，超额保单直接承担（无「未赔足」前提可等待）。
		primary = excess
		excess = nil
		stageName = "独立责任比例(仅超额)"
	}
	weights := make([]int64, len(primary))
	if useIL || stageName != "限额比例" {
		for i, p := range primary {
			weights[i] = p.il
		}
	} else {
		for i, p := range primary {
			w := p.policy.PerLoss
			if w > p.remaining {
				w = p.remaining
			}
			weights[i] = w
		}
	}
	if len(primary) > 0 {
		d.stage1 = settleStage(stageName, primary, weights, amount)
	}

	paid := int64(0)
	if d.stage1 != nil {
		paid = d.stage1.paid
	}

	// 第二阶段：仅在非超额组未赔足时，超额保单按独立责任额接棒。
	if paid < amount && len(excess) > 0 {
		weights := make([]int64, len(excess))
		for i, p := range excess {
			weights[i] = p.il
		}
		d.stage2 = settleStage("独立责任比例(超额)", excess, weights, amount-paid)
	}
	return d
}

// sortParticipants 按保单编号字典序排序，保证裁定与日志可复现。
func sortParticipants(parts []*participant) {
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].policy.PolicyNo < parts[j].policy.PolicyNo
	})
}
