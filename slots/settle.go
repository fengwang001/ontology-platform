package slots

import "time"

// usage 计算单个系列的使用率要素：executed 为实际执行次数（分子），
// planned 为计划次数（分母）。分母 = 系列周数 - 截止前返还周数 - 豁免周数；
// 截止后返还或未执行的周计入分母而不计入分子；豁免周既不计分子也不计分母；
// 分母为零时视为达标。开销只随系列周数增长。
func (c *Coordinator) usage(s *Series) (executed, planned int, qualified bool) {
	for i := range s.status {
		c.weeksScanned++
		switch {
		case s.returned[i] == 1:
			// 截止前返还：不计入分母。
		case s.returned[i] == 2:
			// 截止后返还：计入分母，不计入分子。
			planned++
		case s.status[i] == Exempt:
			// 豁免：不计入分子也不计入分母。
		default:
			planned++
			if s.status[i] == Executed {
				executed++
			}
		}
	}
	qualified = planned == 0 ||
		executed*100 >= c.cfg.UsageThresholdPercent*planned
	return executed, planned, qualified
}

// Usage 返回单个系列的使用率要素：实际执行次数、计划次数与是否达标。
func (c *Coordinator) Usage(seriesID int) (executed, planned int, qualified bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seriesID < 0 || seriesID >= len(c.series) {
		return 0, 0, false, ErrNotFound
	}
	executed, planned, qualified = c.usage(c.series[seriesID])
	return executed, planned, qualified, nil
}

// Settle 在航季结束时结算：所有未登记的周按未执行处理，为每个系列计算
// 使用率，产生下一航季的历史资格清单（按系列 ID 次序）。结算后本航季
// 不可再登记或返还。
func (c *Coordinator) Settle(now time.Time) ([]Eligibility, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	if c.settled {
		return nil, ErrSeasonSettled
	}
	if !c.allocated {
		return nil, ErrNotAllocated
	}
	if now.Before(c.seasonEnd()) {
		return nil, ErrSeasonNotEnded
	}
	var list []Eligibility
	for _, s := range c.series {
		if _, _, qualified := c.usage(s); qualified {
			list = append(list, Eligibility{
				Carrier:   s.Holder,
				Weekday:   s.Weekday,
				Hour:      s.Hour,
				StartWeek: s.StartWeek,
				EndWeek:   s.EndWeek,
			})
		}
	}
	c.settled = true
	c.advance(now)
	return list, nil
}
