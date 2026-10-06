package sheet

// validNow 校验 now 是否在 [0, MaxNow]。
func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// validateEdits 校验 Apply 的编辑列表：1..50 条、键非空且互不相同、
// 写入值在合法范围内。
func validateEdits(edits []Edit) bool {
	if len(edits) == 0 || len(edits) > MaxEdits {
		return false
	}
	seen := make(map[string]struct{}, len(edits))
	for _, e := range edits {
		if e.Key == "" {
			return false
		}
		if _, dup := seen[e.Key]; dup {
			return false
		}
		seen[e.Key] = struct{}{}
		if !e.Clear && (e.Value < MinValue || e.Value > MaxValue) {
			return false
		}
	}
	return true
}

// clockOK 校验 now 是否不小于上一次被接受操作的 now。
func (s *Service) clockOK(now int64) bool {
	return !s.clockSet || now >= s.lastNow
}

// acceptClock 在接受操作时推进时钟。
func (s *Service) acceptClock(now int64) {
	s.lastNow = now
	s.clockSet = true
}
