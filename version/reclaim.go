package version

import "ontology/txid"

// ReclaimBefore 在屏障 barrier 下安全缩短版本链。
//
// 前置条件（由调用方 reclaim 包保证）：
//   - barrier 是“所有活跃快照都不可能再看到”的事务号上界：
//     任何 commit < barrier 的版本对每个活跃快照都已确定可见或确定不可见，
//     且将来不会出现能看到它的新快照；
//   - 只有被“更新的可见版本完全遮蔽”的版本才允许回收。
//
// 规则：链头（最新版本）永远保留，因为它尚未被任何更新版本遮蔽。
// 从最新向最旧行走，保留第一个 commit < barrier 的版本作为遮蔽锚点，
// 它更旧（连续）的尾部版本一律回收——锚点对所有当前/未来快照都是可见结果，
// 更旧版本不可能再被任何人选中。返回被回收的版本数与本次实际考察的版本数。
func (c *Chain) ReclaimBefore(barrier txid.TxID) (removed int, examined int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	anchor := c.head // 链头始终保留
	cur := c.head
	for cur != nil {
		examined++
		if cur.commit < barrier {
			anchor = cur
			break
		}
		cur = cur.older
	}
	if anchor == nil || anchor.older == nil {
		return 0, examined
	}

	// 统计锚点之后的连续尾部长度并直接解链。
	tail := anchor.older
	count := 0
	for e := tail; e != nil; e = e.older {
		count++
	}
	anchor.older = nil
	c.length -= count
	return count, examined
}
