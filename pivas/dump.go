package pivas

import (
	"fmt"
	"sort"
	"strings"
)

// DumpState 输出全系统确定性的规范文本快照：
// 相同操作序列重放得到完全相同的输出。
func (c *Center) DumpState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sb strings.Builder
	last := int64(-1)
	if c.hasNow {
		last = c.lastNow
	}
	fmt.Fprintf(&sb, "lastNow=%d\n", last)
	for _, benchID := range c.benchOrder {
		bn := c.benches[benchID]
		for _, ba := range bn.queue {
			ids := make([]string, 0, len(ba.orders))
			for _, r := range ba.orders {
				ids = append(ids, r.id)
			}
			fmt.Fprintf(&sb, "batch %s bench=%s start=%d end=%d solvent=%s lightProof=%v orders=[%s]\n",
				ba.id, benchID, ba.start, bn.end(ba), ba.solvent, ba.lightProof, strings.Join(ids, ","))
		}
	}
	ids := make([]string, 0, len(c.orders))
	for id := range c.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := c.orders[id]
		state := "scheduled"
		if r.done {
			state = "completed"
		}
		if r.cancelled {
			state = "cancelled"
		}
		fmt.Fprintf(&sb, "order %s state=%s bench=%s batch=%s storage=%s requiredAt=%d urgent=%v lightProof=%v\n",
			id, state, r.benchID, r.batchID, r.storage, r.requiredAt, r.urgent, r.lightProof)
	}
	return sb.String()
}

// VerifyInvariants 校验系统级不变量，供测试与运行自检：
// 任一已受理医嘱在任意时刻都满足按时与有效期两项约束，
// 且批次编组、容量、清场间隔、时长对照均合法。
func (c *Center) VerifyInvariants() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, benchID := range c.benchOrder {
		bn := c.benches[benchID]
		for i, ba := range bn.queue {
			if len(ba.orders) == 0 || len(ba.orders) > bn.cfg.Capacity {
				return fmt.Errorf("批次 %s 数量越界: %d", ba.id, len(ba.orders))
			}
			if i+1 < len(bn.queue) {
				if bn.end(ba)+bn.cfg.ClearanceSec > bn.queue[i+1].start {
					return fmt.Errorf("批次 %s 与 %s 之间清场间隔不足", ba.id, bn.queue[i+1].id)
				}
			}
			end := bn.end(ba)
			for _, r := range ba.orders {
				if r.cancelled {
					return fmt.Errorf("批次 %s 含已取消医嘱 %s", ba.id, r.id)
				}
				if r.solvent != ba.solvent || r.lightProof != ba.lightProof {
					return fmt.Errorf("医嘱 %s 与批次 %s 溶媒或外袋形态不一致", r.id, ba.id)
				}
				if end+r.transport > r.requiredAt {
					return fmt.Errorf("医嘱 %s 无法按时送达: 送达 %d 要求 %d", r.id, end+r.transport, r.requiredAt)
				}
				if r.transport >= r.stable() {
					return fmt.Errorf("医嘱 %s 送达时已失效", r.id)
				}
			}
		}
	}
	for _, r := range c.orders {
		if r.cancelled || r.done {
			continue
		}
		bn := c.benches[r.benchID]
		found := false
		for _, ba := range bn.queue {
			if ba.id == r.batchID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("医嘱 %s 的批次 %s 不在队列中", r.id, r.batchID)
		}
	}
	return nil
}
