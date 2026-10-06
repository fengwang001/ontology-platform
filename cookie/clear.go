package cookie

import "time"

// 本文件实现三类清除操作，全部计入显式删除，
// 与过期淘汰、容量淘汰可区分查询。

// ClearSite 移除某站点的全部条目，返回移除条数。
func (s *Store) ClearSite(site string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	si := s.sites[site]
	if si == nil {
		return 0
	}
	n := 0
	for k := range si.items {
		s.removeLocked(k, &s.stats.ExplicitDeletes)
		n++
	}
	return n
}

// ClearPartition 移除分区键等于给定值的全部条目（nil 匹配未分区条目）。
func (s *Store) ClearPartition(partition *string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ks []key
	for k, e := range s.entries {
		if partitionEqual(e.PartitionKey, partition) {
			ks = append(ks, k)
		}
	}
	for _, k := range ks {
		s.removeLocked(k, &s.stats.ExplicitDeletes)
	}
	return len(ks)
}

// ClearCreatedRange 按创建时刻清除，区间左闭右开 [start, end)。
func (s *Store) ClearCreatedRange(start, end time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if end.Before(start) {
		return 0, ErrInvalidArgument
	}
	var ks []key
	for k, e := range s.entries {
		if !e.CreatedAt.Before(start) && e.CreatedAt.Before(end) {
			ks = append(ks, k)
		}
	}
	for _, k := range ks {
		s.removeLocked(k, &s.stats.ExplicitDeletes)
	}
	return len(ks), nil
}
