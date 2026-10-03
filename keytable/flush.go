package keytable

import (
	"sort"

	"ontology/report"
)

// Pending 返回某键当前未交付的丢弃数与条目是否存在。
func (s *Sampler) Pending(tenantName, key string) (dropped int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, exists := s.tenants[tenantName]; exists {
		if n, hit := t.nodes[key]; hit {
			return n.e.Dropped, true
		}
	}
	return 0, false
}

// Flush 交付所有已结束窗口（win < ⌊now/W⌋）且仍有丢弃计数的条目，
// 按 (租户, 键) 字节序；sink 返回错误即停止，失败项及其后条目保持原样。
func (s *Sampler) Flush(now int64, sink report.Sink) (int, error) {
	if sink == nil {
		return 0, report.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return 0, report.ErrClockSkew
	}
	s.maxNow = now
	cur := now / s.w

	tenantNames := make([]string, 0, len(s.tenants))
	for name := range s.tenants {
		tenantNames = append(tenantNames, name)
	}
	sort.Strings(tenantNames)

	delivered := 0
	for _, name := range tenantNames {
		t := s.tenants[name]
		keys := make([]string, 0, len(t.nodes))
		for k := range t.nodes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n := t.nodes[k]
			if n.e.Win >= cur || n.e.Dropped == 0 {
				continue
			}
			sum := report.Summary{
				Tenant: name, Key: k, Window: n.e.Win,
				Dropped: n.e.Dropped, Reason: report.Closed,
			}
			if err := sink(sum); err != nil {
				return delivered, err
			}
			n.e.Dropped = 0
			delivered++
		}
	}
	return delivered, nil
}

// examinedEntries 返回累计被考察的已存在键表条目数（供同包测试断言）。
func (s *Sampler) examinedEntries() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.examined
}
