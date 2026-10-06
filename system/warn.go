package system

import (
	"fmt"
	"sort"

	"ontology/domain"
)

// Warn 是只读查询：列出 date 当日起各类别预警提前天数内（含边界）到期、
// 且未封存未停用的对象。附件临近到期时，其所在的在用设备也列出，
// 并在 Via 中给出触发的附件编号（升序）。
//
// 排序：按触发到期日升序（设备仅被附件触发时取触发附件中最早到期日），
// 并列按编号升序。不检查日期回退，也不推进时钟。
//
// 复杂度：每个类别桶一次二分定位加命中前缀扫描，总开销 O(log n + k)，
// k 为命中条数，与对象总数无关（索引仅含在用对象）。
func (s *System) Warn(date int) ([]WarnEntry, error) {
	if date < 0 {
		return nil, domain.NewError(domain.ErrInvalidParam, fmt.Sprintf("日期非法: %d", date))
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	self := make(map[string]int)     // 自身命中的对象 id -> 到期日
	via := make(map[string][]string) // 设备 id -> 触发附件编号
	viaMin := make(map[string]int)   // 设备 id -> 触发附件最早到期日

	for cat, cfg := range s.configs {
		entries, _, _ := s.idx.Range(cat, date, date+cfg.WarnAheadDays)
		for _, e := range entries {
			self[e.ID] = e.Expiry
			obj := s.objects[e.ID]
			if obj.Cat.IsAccessory() && obj.HostID != "" {
				host := s.objects[obj.HostID]
				if host.Status == domain.StatusInService {
					via[host.ID] = append(via[host.ID], obj.ID)
					if m, ok := viaMin[host.ID]; !ok || e.Expiry < m {
						viaMin[host.ID] = e.Expiry
					}
				}
			}
		}
	}

	type item struct {
		entry   WarnEntry
		sortKey int
	}
	items := make([]item, 0, len(self)+len(via))
	seen := make(map[string]bool)
	for id, expiry := range self {
		obj := s.objects[id]
		it := item{entry: WarnEntry{ID: id, Cat: obj.Cat, Expiry: expiry}, sortKey: expiry}
		if v, ok := via[id]; ok {
			sort.Strings(v)
			it.entry.Via = v
		}
		items = append(items, it)
		seen[id] = true
	}
	for id, v := range via {
		if seen[id] {
			continue
		}
		obj := s.objects[id]
		sort.Strings(v)
		items = append(items, item{
			entry:   WarnEntry{ID: id, Cat: obj.Cat, Expiry: obj.Expiry, Via: v},
			sortKey: viaMin[id],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].sortKey != items[j].sortKey {
			return items[i].sortKey < items[j].sortKey
		}
		return items[i].entry.ID < items[j].entry.ID
	})
	out := make([]WarnEntry, len(items))
	for i, it := range items {
		out[i] = it.entry
	}
	return out, nil
}
