package slots

import (
	"sort"
	"time"
)

// seriesUsage 计算单个系列的实际执行次数（分子）与计划次数（分母）。
//
// 对每个仍持有的周：
//   - 豁免周：分子分母均不计；
//   - 登记为已执行：分子与分母各计一；
//   - 其余（含未登记，结算时按未执行）：只计入分母。
//
// 返还截止时刻（取闭）前返还的周整周剔除（不占分母，也不看登记）。
// 仅遍历 [StartWeek,EndWeek]，复杂度 O(系列周数)，与其他规模无关。
func seriesUsage(cfg Config, s *Series) (used, planned int) {
	for w := s.StartWeek; w <= s.EndWeek; w++ {
		if s.returned[w] &&
			(cfg.ReturnDeadline.IsZero() || !s.returnedAt[w].After(cfg.ReturnDeadline)) {
			continue
		}
		if s.register[w] == RegExempt {
			continue
		}
		planned++
		if s.register[w] == RegExecuted {
			used++
		}
	}
	return used, planned
}

// qualifies 实现“使用率不低于达标比例，恰等于视为达标；分母为零视为达标”。
// used*100 >= pct*planned，避免浮点误差。
func qualifies(cfg Config, used, planned int) bool {
	if planned == 0 {
		return true
	}
	return used*100 >= cfg.QualifyPercent*planned
}

// SettleSeason 航季结束时结算：未登记周按未执行处理，产出下航季历史资格清单。
func (c *Coordinator) SettleSeason(at time.Time) ([]Qualification, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tracef(c.log, "SettleSeason in {at:%s}", at.Format(time.RFC3339))
	if err := c.advance(at); err != nil {
		tracef(c.log, "SettleSeason out -> %s", err)
		return nil, err
	}
	if c.phase == phaseSettled {
		tracef(c.log, "SettleSeason out -> %s", ErrSeasonSettled)
		return nil, ErrSeasonSettled
	}
	if c.phase != phaseAllocated || at.Before(c.cfg.SeasonEnd) {
		tracef(c.log, "SettleSeason out -> %s", ErrApplyClosed)
		return nil, ErrApplyClosed
	}
	var qs []Qualification
	for _, s := range sortedSeries(c.series) {
		used, planned := seriesUsage(c.cfg, s)
		qs = append(qs, Qualification{
			SeriesID: s.ID, Airline: s.Airline,
			Day: s.Day, Hour: s.Hour,
			StartWeek: s.StartWeek, EndWeek: s.EndWeek,
			Used: used, Planned: planned,
			Qualified: qualifies(c.cfg, used, planned),
		})
	}
	c.quals = qs
	c.phase = phaseSettled
	c.commit(at)
	tracef(c.log, "SettleSeason out -> series:%d", len(qs))
	return append([]Qualification(nil), qs...), nil
}

// Snapshot 返回确定性的只读状态快照（系列与名单均按键排序）。
func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Coordinator) snapshotLocked() Snapshot {
	snap := Snapshot{
		Series:         make([]SeriesSnap, 0, len(c.series)),
		Waitlist:       []WaitlistSnap{},
		Qualifications: append([]Qualification(nil), c.quals...),
	}
	switch c.phase {
	case phaseAccepting:
		snap.Phase = "accepting"
	case phaseAllocated:
		snap.Phase = "allocated"
	case phaseSettled:
		snap.Phase = "settled"
	}
	for _, s := range sortedSeries(c.series) {
		var rs []int
		for w := s.StartWeek; w <= s.EndWeek; w++ {
			if s.returned[w] {
				rs = append(rs, w)
			}
		}
		reg := map[int]RegStatus{}
		for w, st := range s.register {
			reg[w] = st
		}
		snap.Series = append(snap.Series, SeriesSnap{
			ID: s.ID, Airline: s.Airline, Day: s.Day, Hour: s.Hour,
			StartWeek: s.StartWeek, EndWeek: s.EndWeek,
			Returned: rs, Register: reg,
		})
	}
	keys := make([]slotKey, 0, len(c.wait))
	for k := range c.wait {
		if len(c.wait[k]) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return keys[i].hour < keys[j].hour
	})
	for _, k := range keys {
		ids := make([]string, 0, len(c.wait[k]))
		for _, e := range c.wait[k] {
			ids = append(ids, e.app.ID)
		}
		snap.Waitlist = append(snap.Waitlist, WaitlistSnap{
			Day: k.day, Hour: k.hour, ApplicationIDs: ids,
		})
	}
	return snap
}
