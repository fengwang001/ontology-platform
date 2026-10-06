package pricing

import (
	"fmt"
	"sort"
	"time"
)

type Rate int64

type DayType int

const (
	Weekday DayType = iota
	Weekend
	Holiday
)

type Period struct {
	StartMinute int
	EndMinute   int
	Rate        Rate
}

type Schedule map[DayType][]Period

type TariffVersion struct {
	Effective time.Time
	Schedule  Schedule
}

type tariffBook struct {
	versions []TariffVersion
}

func validateSchedule(schedule Schedule) error {
	if len(schedule) != 3 {
		return errInvalid("日类型时段表不完整")
	}
	for day := Weekday; day <= Holiday; day++ {
		periods := schedule[day]
		if len(periods) == 0 {
			return errInvalid("时段表为空")
		}
		expectedStart := 0
		for index, period := range periods {
			if period.StartMinute < 0 || period.EndMinute > 24*60 || period.StartMinute >= period.EndMinute {
				return errInvalid("时段边界越界")
			}
			if period.StartMinute != expectedStart {
				return errInvalid("时段表必须无缝覆盖整日")
			}
			if period.Rate < 0 {
				return errInvalid("单价不能为负")
			}
			expectedStart = period.EndMinute
			if index == len(periods)-1 && expectedStart != 24*60 {
				return errInvalid("时段表必须恰好覆盖整日")
			}
		}
	}
	return nil
}

func (b *tariffBook) insert(version TariffVersion) error {
	if version.Effective.IsZero() {
		return errInvalid("生效时刻越界")
	}
	if err := validateSchedule(version.Schedule); err != nil {
		return err
	}
	for _, existing := range b.versions {
		if existing.Effective.Equal(version.Effective) {
			return errInvalid("版本生效时刻重复")
		}
	}
	b.versions = append(b.versions, version)
	sort.Slice(b.versions, func(i, j int) bool {
		return b.versions[i].Effective.Before(b.versions[j].Effective)
	})
	return nil
}

func (b *tariffBook) versionAt(at time.Time) (TariffVersion, bool) {
	index := sort.Search(len(b.versions), func(i int) bool {
		return b.versions[i].Effective.After(at)
	}) - 1
	if index < 0 {
		return TariffVersion{}, false
	}
	return b.versions[index], true
}

func periodRate(schedule Schedule, day DayType, at time.Time) Rate {
	minute := at.Hour()*60 + at.Minute()
	for _, period := range schedule[day] {
		if minute >= period.StartMinute && minute < period.EndMinute {
			return period.Rate
		}
	}
	panic(fmt.Sprintf("invalid schedule for day type %d", day))
}
