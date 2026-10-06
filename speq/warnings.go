package speq

import "sort"

// QueryWarnings 查询 date 日起「预警提前天数」内到期（含边界）且未封存、
// 未停用的对象。
//
// 返回规则：
//   - 对象自身命中（date <= expiry <= date+该类别预警提前天数）时列出；
//   - 设备若因附件临近到期，设备同时列出，Triggers 给出触发附件编号（升序）；
//   - 设备自身命中与附件触发合并为一条，SortExpiry = min(自身到期日, 触发到期日)；
//   - 排序：SortExpiry 升序，并列按编号升序。
//
// 复杂度：只扫描可能命中的索引区间 + 命中设备的附件，
// 不随对象总数线性增长（外加一个与命中设备数相关的对数级排序）。
func (s *System) QueryWarnings(date int) ([]WarningEntry, error) {
	if date < 0 {
		return nil, errf(ErrInvalidParameter, "预警日期不能为负: %d", date)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	maxLead := 0
	for _, cfg := range s.categories {
		if cfg.WarningLeadDays > maxLead {
			maxLead = cfg.WarningLeadDays
		}
	}
	hi := date + maxLead

	entries := make(map[string]*WarningEntry)
	// deviceTrig[deviceID] = 触发附件到期日（取最早）。
	deviceTrig := make(map[string]int)
	selfHit := make(map[string]bool)

	ensure := func(id string) *WarningEntry {
		e := entries[id]
		if e == nil {
			o := s.objects[id]
			e = &WarningEntry{ID: id, Kind: o.kind, Expiry: o.expiry, SortExpiry: o.expiry}
			entries[id] = e
		}
		return e
	}

	s.expiryIndex.scanAsc(date, hi, func(expiry int, id string) bool {
		o := s.objects[id]
		cfg := s.categories[o.category]
		hitSelf := expiry <= date+cfg.WarningLeadDays
		if o.kind == KindDevice {
			if hitSelf {
				ensure(id)
				selfHit[id] = true
			}
			return true
		}
		// 附件：自身命中则作为独立条目列出。
		if hitSelf {
			ensure(id)
			selfHit[id] = true
		}
		// 无论附件自身是否命中（预警窗口取附件类别），只要挂在设备上，
		// 就按附件自身的预警窗口判定是否触发其设备。
		if o.host != "" && hitSelf {
			d := s.objects[o.host]
			if d != nil && s.indexed(d) {
				if prev, ok := deviceTrig[o.host]; !ok || expiry < prev {
					deviceTrig[o.host] = expiry
				}
			}
		}
		return true
	})

	// 汇总设备触发附件，生成/合并设备条目。
	deviceIDs := make([]string, 0, len(deviceTrig))
	for id := range deviceTrig {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Strings(deviceIDs)
	for _, did := range deviceIDs {
		// 设备自身已超期也可被附件触发列出（对象仍未封存/停用）。
		e := ensure(did)
		var triggers []string
		for aid := range s.attachments[did] {
			a := s.objects[aid]
			if a == nil || !s.indexed(a) {
				continue
			}
			acfg := s.categories[a.category]
			if date <= a.expiry && a.expiry <= date+acfg.WarningLeadDays {
				triggers = append(triggers, aid)
			}
		}
		sort.Strings(triggers)
		e.Triggers = triggers
		if len(triggers) == 0 {
			// 理论上不会发生（deviceTrig 由扫描得出）。
			delete(entries, did)
			continue
		}
		earliest := deviceTrig[did]
		if !selfHit[did] {
			e.SortExpiry = earliest
		} else if earliest < e.SortExpiry {
			e.SortExpiry = earliest
		}
	}

	out := make([]WarningEntry, 0, len(entries))
	for _, e := range entries {
		if e.Kind == KindDevice && !selfHit[e.ID] && len(e.Triggers) == 0 {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortExpiry != out[j].SortExpiry {
			return out[i].SortExpiry < out[j].SortExpiry
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
