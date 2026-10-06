package speq

import "sort"

// Get 返回对象快照；ok=false 表示对象不存在。
func (s *System) Get(id string) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{
		ID:       o.id,
		Category: o.category,
		Kind:     o.kind,
		Scrapped: o.scrapped,
		Sealed:   o.sealed,
		Disabled: o.disabled,
		Expiry:   o.expiry,
		Host:     o.host,
	}, true
}

// Snapshot 返回全部对象快照，按编号升序。
func (s *System) Snapshot() []Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		o := s.objects[id]
		out = append(out, Snapshot{
			ID:       o.id,
			Category: o.category,
			Kind:     o.kind,
			Scrapped: o.scrapped,
			Sealed:   o.sealed,
			Disabled: o.disabled,
			Expiry:   o.expiry,
			Host:     o.host,
		})
	}
	return out
}

// AttachmentsOf 返回设备当前挂接附件编号（升序）。
func (s *System) AttachmentsOf(deviceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.attachments[deviceID]
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// LastDate 返回上一个被接受操作的日期（初始为 -1）。
func (s *System) LastDate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastDate
}

// IndexSize 返回到期索引中的条目数（供测试与性能验证使用）。
func (s *System) IndexSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiryIndex.size
}

// IndexDepth 返回到期索引的树高（供测试验证对数级高度）。
func (s *System) IndexDepth() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiryIndex.depth()
}
