package store

import (
	"fmt"

	"ontology/txid"
)

// Reclaim 执行一次增量回收：只考察候选堆中已就绪的版本，
// 不做全表扫描。返回本次本轮实际回收的版本数。
func (s *Store) Reclaim() int {
	cands := s.rec.Collect()
	if len(cands) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range cands {
		chain, ok := s.chains[c.Key]
		if !ok {
			continue
		}
		if chain.Remove(c.Commit) { // 安全移除：最新版本永不命中
			s.removeIndexEntryLocked(c.Key, c.Commit)
			s.total--
			n++
		}
	}
	return n
}

// Stats 是全局只读查询结果。
type Stats struct {
	OpenSnapshots int
	WaterLevel    txid.ID
	TotalVersions int
	Examined      int64 // 历次回收实际考察的版本总数
}

// Stats 查询全局状态。纯只读，不推进任何状态。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	total := s.total
	s.mu.RUnlock()
	return Stats{
		OpenSnapshots: s.snaps.Count(),
		WaterLevel:    s.rec.WaterLevel(),
		TotalVersions: total,
		Examined:      s.rec.Examined(),
	}
}

// KeyVersions 查询某键当前已提交（可见）版本数。
// 已回收或从未存在的键返回 0。纯只读。
func (s *Store) KeyVersions(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chainLen(key)
}

// checkConsistency 校验索引与版本链一致：
// 每个索引槽位都有对应版本体，且顺序一致。仅供测试与演示。
func (s *Store) checkConsistency() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.index) != len(s.chains) {
		return fmt.Errorf("索引键数 %d 与版本链键数 %d 不一致", len(s.index), len(s.chains))
	}
	for key, entries := range s.index {
		chain := s.chains[key]
		if chain == nil {
			return fmt.Errorf("键 %s 有索引但无版本链", key)
		}
		vs := chain.Committed()
		if len(entries) != len(vs) {
			return fmt.Errorf("键 %s 索引槽位 %d 与版本体 %d 不一致", key, len(entries), len(vs))
		}
		for i, v := range vs {
			if entries[i] != v {
				return fmt.Errorf("键 %s 第 %d 个索引槽位未指向版本体", key, i)
			}
		}
	}
	return nil
}
