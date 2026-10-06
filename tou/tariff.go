package tou

import (
	"sort"
	"time"
)

// validateSchedule 校验一张日类型时段表必须恰好无缝覆盖整日：
// 起点为 0、严格递增、终点为 86400、单价非负。
func validateSchedule(s DaySchedule) bool {
	if len(s.Periods) == 0 || s.Periods[0].StartSec != 0 {
		return false
	}
	for i, p := range s.Periods {
		if p.Price < 0 {
			return false
		}
		if i+1 < len(s.Periods) {
			if s.Periods[i+1].StartSec <= p.StartSec {
				return false
			}
		}
	}
	return s.Periods[len(s.Periods)-1].StartSec < int(secondsPerDay)
}

func periodEnd(s DaySchedule, idx int) int {
	if idx+1 < len(s.Periods) {
		return s.Periods[idx+1].StartSec
	}
	return int(secondsPerDay)
}

func validateVersion(v TariffVersion) bool {
	if !validMoment(v.EffectiveAt) {
		return false
	}
	for _, s := range v.Schedules {
		if !validateSchedule(s) {
			return false
		}
	}
	return true
}

// tariffBook 是按生效时刻排序的电价表版本册。
type tariffBook struct {
	versions []TariffVersion
}

func (b *tariffBook) add(v TariffVersion) bool {
	i := sort.Search(len(b.versions), func(i int) bool {
		return !b.versions[i].EffectiveAt.Before(v.EffectiveAt)
	})
	if i < len(b.versions) && b.versions[i].EffectiveAt.Equal(v.EffectiveAt) {
		return false
	}
	b.versions = append(b.versions, TariffVersion{})
	copy(b.versions[i+1:], b.versions[i:])
	b.versions[i] = v
	return true
}

// at 返回时刻 t 适用的版本及其在册中索引；无版本可用时 ok=false。
func (b *tariffBook) at(t time.Time) (TariffVersion, int, bool) {
	i := sort.Search(len(b.versions), func(i int) bool {
		return b.versions[i].EffectiveAt.After(t)
	}) - 1
	if i < 0 {
		return TariffVersion{}, -1, false
	}
	return b.versions[i], i, true
}

// periodAt 返回 t 当日指定日类型时段表中覆盖 t 的时段下标。
func periodAt(s DaySchedule, t time.Time) int {
	sec := t.Hour()*3600 + t.Minute()*60 + t.Second()
	i := sort.Search(len(s.Periods), func(i int) bool {
		return s.Periods[i].StartSec > sec
	}) - 1
	return i
}
