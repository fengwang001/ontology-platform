package logstore

// SegmentInfo 暴露段布局快照，仅供测试与观察使用。
type SegmentInfo struct {
	ID     int
	Used   uint64
	Live   uint64
	Sealed bool
	MaxTS  uint64
}

// Snapshot 返回当前段布局与逻辑时钟的有序快照。
func (s *Store) Snapshot() (segments []SegmentInfo, clock uint64, activeID int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int, 0, len(s.segments))
	for id := range s.segments {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	for _, id := range ids {
		seg := s.segments[id]
		segments = append(segments, SegmentInfo{
			ID: seg.id, Used: seg.used, Live: s.live[id],
			Sealed: seg.sealed, MaxTS: seg.maxTS,
		})
	}
	return segments, s.clock, s.activeID
}

// verifyAccounting 校验不变式：
// 每段存活字节 == 索引指向该段的最新值块字节 + 该段存活墓碑字节。
func (s *Store) verifyAccounting() bool {
	want := make(map[int]uint64)
	for _, locs := range s.index {
		for i, l := range locs {
			if l.kind == kindValue {
				if i == len(locs)-1 {
					want[l.segID] += l.size
				}
			} else {
				alive := false
				for _, other := range locs {
					if other.ts < l.ts && other.segID != l.segID {
						alive = true
						break
					}
				}
				if alive {
					want[l.segID] += l.size
				}
			}
		}
	}
	for id, seg := range s.segments {
		if want[id] != s.live[id] {
			return false
		}
		if seg.used > 0 && len(seg.blocks) == 0 {
			return false
		}
	}
	for id, v := range s.live {
		if _, ok := s.segments[id]; !ok && v != 0 {
			return false
		}
	}
	return true
}
