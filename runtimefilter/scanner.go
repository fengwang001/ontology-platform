package runtimefilter

import (
	"cmp"
	"sync"
	"time"
)

// Stats 是一个扫描器的累计计数；恒有 Passed+Dropped==Scanned。
type Stats struct {
	Scanned int
	Passed  int
	Dropped int
}

// Scanner 表示一个探测侧扫描。
type Scanner[K cmp.Ordered] struct {
	coord *Coordinator[K]
	name  string
	keyOf KeyOf[K]

	mu      sync.Mutex
	scanned int
	passed  int
	dropped int
}

// FilterBatch 对一批行应用当前过滤器版本，返回放行的行与该批判定信息。
//
// 就绪前 / 不允许过滤 / 等待超时 / 过滤器作废：整批原样放行；
// 就绪后：丢弃空键行与不在摘要内的行；构建侧全空时整批丢弃（内连接/半连接）。
// 每一批只使用一个完整的过滤器版本，绝不会看到合并一半的摘要。
func (s *Scanner[K]) FilterBatch(rows []Row, deadline time.Time) ([]Row, Stats) {
	f := s.coord.waitReady(deadline)

	var st Stats
	st.Scanned = len(rows)

	if f == nil {
		st.Passed = len(rows)
		s.accumulate(st)
		s.coord.logf("scan=%s batch input=%d output=%d passed=%d dropped=%d decision=pass-all reason=%s",
			s.name, st.Scanned, st.Passed, st.Passed, st.Dropped, s.passAllReason())
		return append([]Row(nil), rows...), st
	}

	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		k, ok := s.keyOf(row)
		if !ok {
			// 空键在任何连接类型下都不可能与构建侧匹配。
			s.coord.logf("scan=%s row=%v decision=drop reason=null-key", s.name, row)
			continue
		}
		if f.buildEmpty || !f.contains(k) {
			s.coord.logf("scan=%s row=%v key=%v decision=drop reason=not-in-summary", s.name, row, k)
			continue
		}
		s.coord.logf("scan=%s row=%v key=%v decision=pass reason=in-summary version=%d",
			s.name, row, k, f.version)
		out = append(out, row)
	}
	st.Passed = len(out)
	st.Dropped = st.Scanned - st.Passed
	s.accumulate(st)
	s.coord.logf("scan=%s batch input=%d output=%d passed=%d dropped=%d decision=filter version=%d",
		s.name, st.Scanned, st.Passed, st.Passed, st.Dropped, f.version)
	return out, st
}

// Stats 返回该扫描器的累计计数。
func (s *Scanner[K]) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Scanned: s.scanned, Passed: s.passed, Dropped: s.dropped}
}

func (s *Scanner[K]) accumulate(st Stats) {
	s.mu.Lock()
	s.scanned += st.Scanned
	s.passed += st.Passed
	s.dropped += st.Dropped
	s.mu.Unlock()
}

func (s *Scanner[K]) passAllReason() string {
	if !s.coord.enabled {
		return "filter-not-allowed(" + s.coord.cfg.JoinType.String() + ")"
	}
	return "not-ready-or-timeout"
}

// contains 判定键是否落在已发布的完整摘要内。
func (f *filter[K]) contains(k K) bool {
	if f.distinct != nil {
		_, ok := f.distinct[k]
		return ok
	}
	return k >= f.min && k <= f.max
}
