package contract

import (
	"fmt"
	"sort"
)

// signingDeadlineDays 一方签署后对方完成签署的允许天数（恰等于仍可签）。
// 作为服务级策略，可按需参数化；本实现固定为 10 天。
const signingDeadlineDays int64 = 10

func cloneClauses(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneLocked(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *contractState) partyIndex(id string) int {
	for i := range c.parties {
		if c.parties[i].ID == id {
			return i
		}
	}
	return -1
}

// terminatedAt 返回已生效的终止日（双方协商提前终止或尚未发生为 -1）。
// 到期终止是查询时推演出来的，不写入 termDay。
func (c *contractState) terminatedAt() int64 {
	return c.termDay
}

// tryActivate 在签署完成且（如需）会签齐备时，确定协议生效日并建立条款索引。
// 生效日 = max(声明生效日, 签署完成日, 法务会签日)。
// 提前终止后：生效日晚于终止日的协议不生效。
func (c *contractState) tryActivate(a *amendment) {
	if a.effectiveDay >= 0 || a.completedDay < 0 {
		return
	}
	if a.needsCosign && a.cosignedAt < 0 {
		return
	}
	eff := a.effectiveDecl
	if eff < a.completedDay {
		eff = a.completedDay
	}
	if a.needsCosign && eff < a.cosignedAt {
		eff = a.cosignedAt
	}
	if c.termDay >= 0 && eff > c.termDay {
		// 合同已提前终止，生效日落在终止日之后：永不生效。
		return
	}
	a.effectiveDay = eff
	for cid := range a.changes {
		c.byClause[cid] = append(c.byClause[cid], a)
	}
	if a.revokes != "" {
		c.revokerChildren[a.revokes] = append(c.revokerChildren[a.revokes], a)
	}
}

// rebuildClauseIndex 朴素模型/测试重放后按生效日重建索引。
func (c *contractState) rebuildClauseIndex() {
	c.byClause = map[string][]*amendment{}
	c.revokerChildren = map[string][]*amendment{}
	active := make([]*amendment, 0, len(c.order))
	for _, a := range c.order {
		if a.effectiveDay >= 0 {
			active = append(active, a)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].effectiveDay != active[j].effectiveDay {
			return active[i].effectiveDay < active[j].effectiveDay
		}
		return active[i].completionSeq < active[j].completionSeq
	})
	for _, a := range active {
		for cid := range a.changes {
			c.byClause[cid] = append(c.byClause[cid], a)
		}
		if a.revokes != "" {
			c.revokerChildren[a.revokes] = append(c.revokerChildren[a.revokes], a)
		}
	}
}

func (s *Service) record(c *contractState, op, in, out, reason string) {
	if !s.logging {
		return
	}
	s.logs = append(s.logs, OperationLog{
		Seq:    c.globalSeq,
		Op:     op,
		Input:  in,
		Output: out,
		Reason: reason,
	})
}

// SnapshotLog 打印日志为文本，便于审计与调试。
func (s *Service) SnapshotLog() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ""
	for _, l := range s.logs {
		out += fmt.Sprintf("[seq=%d] %s | in=%s | out=%s | why=%s\n",
			l.Seq, l.Op, l.Input, l.Output, l.Reason)
	}
	return out
}
