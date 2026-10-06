package docsync

import "sort"

// testActiveOffsets 返回当前有效诊断的 (seq,start,end)，按 (start,end,seq) 升序。
// 仅供测试：直接读取 treap 中的码元偏移，绕过位置转换。
func (s *Service) testActiveOffsets() []naiveItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := dInorder(s.byStart, nil)
	out := make([]naiveItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, naiveItem{
			seq: e.sequence, start: e.start, end: e.end, message: e.message,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].start != out[j].start {
			return out[i].start < out[j].start
		}
		if out[i].end != out[j].end {
			return out[i].end < out[j].end
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// testDeadMeta 返回失效清单 (seq, bornVersion, failedVersion) 顺序。
func (s *Service) testDeadMeta() [][3]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ordered := deadList(s.dead).sorted()
	out := make([][3]int, 0, len(ordered))
	for _, r := range ordered {
		out = append(out, [3]int{r.sequence, r.bornVersion, r.failedVersion})
	}
	return out
}

// testCU 暴露内部码元总数。
func (s *Service) testCU() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalCU
}
