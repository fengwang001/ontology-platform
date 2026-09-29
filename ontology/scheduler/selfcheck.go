package scheduler

import "fmt"

// SelfCheck 校验内部数据结构不变量：
// 序号连续、写集非空且已去重排序、依赖均指向更早事务且写集确实相交、
// 深度等于依赖最大深度加一、最近写入者映射正确。可被多个执行体并发调用。
func (s *Scheduler) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lastWriter := make(map[string]int)
	for i, t := range s.txns {
		if t.seq != i+1 {
			return fmt.Errorf("selfcheck: tx at index %d has seq %d", i, t.seq)
		}
		if len(t.writeKeys) == 0 {
			return fmt.Errorf("selfcheck: tx %d has empty write set", t.seq)
		}
		for j := 1; j < len(t.writeKeys); j++ {
			if t.writeKeys[j-1] >= t.writeKeys[j] {
				return fmt.Errorf("selfcheck: tx %d write keys not sorted/deduped", t.seq)
			}
		}
		expectedDeps := map[int]struct{}{}
		maxDepth := 0
		for _, k := range t.writeKeys {
			if seq, ok := lastWriter[k]; ok {
				expectedDeps[seq] = struct{}{}
			}
		}
		if len(expectedDeps) != len(t.dependsOn) {
			return fmt.Errorf("selfcheck: tx %d dep count mismatch", t.seq)
		}
		for _, dep := range t.dependsOn {
			if _, ok := expectedDeps[dep]; !ok {
				return fmt.Errorf("selfcheck: tx %d has spurious dependency on tx %d", t.seq, dep)
			}
			if dep >= t.seq {
				return fmt.Errorf("selfcheck: tx %d depends on later tx %d", t.seq, dep)
			}
			if d := s.txns[dep-1].depth; d > maxDepth {
				maxDepth = d
			}
		}
		if t.depth != maxDepth+1 {
			return fmt.Errorf("selfcheck: tx %d depth %d want %d", t.seq, t.depth, maxDepth+1)
		}
		for _, k := range t.writeKeys {
			lastWriter[k] = t.seq
		}
	}
	for k, seq := range s.lastWriter {
		if lastWriter[k] != seq {
			return fmt.Errorf("selfcheck: last writer for key %q inconsistent", k)
		}
	}
	return nil
}
