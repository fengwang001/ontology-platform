// Package tou implements a time-of-use electricity settlement engine.
package tou

import (
	"errors"
	"time"
)

// 可区分的错误类别。拒绝次序（固定）：
// ErrInvalidParameter > ErrMonthClosed > ErrOutOfOrder >
// ErrReadingRegression > ErrInsufficientReadings。
// ErrUnbillable 只在封账时产生，先于 ErrInsufficientReadings 检查。
var (
	ErrInvalidParameter     = errors.New("参数非法")
	ErrMonthClosed          = errors.New("月份已封账")
	ErrOutOfOrder           = errors.New("时序错误")
	ErrReadingRegression    = errors.New("读数倒退")
	ErrInsufficientReadings = errors.New("读数不足")
	ErrUnbillable           = errors.New("不可计价片阻止封账")
)

// DayType 是日类型。节假日 > 休息日 > 工作日。
type DayType int

const (
	DayWorkday DayType = iota
	DayWeekend
	DayHoliday
)

func (d DayType) String() string {
	switch d {
	case DayWeekend:
		return "休息日"
	case DayHoliday:
		return "节假日"
	default:
		return "工作日"
	}
}

// Period 是一日时段表中的一个左闭右开时段：
// [StartSec, 下一时刻)，最后一个时段结束于 86400（次日 0 点）。
// StartSec 单位为当日 0 点起的秒数；Price 为每 Wh 的千分之一货币单位，
// 即最小货币单位/1000/Wh，金额向下取整到最小货币单位。
type Period struct {
	StartSec int
	Price    int64
}

// DaySchedule 是某一日类型的一日时段表，必须恰好无缝覆盖 [0,86400)。
type DaySchedule struct {
	Periods []Period
}

// TariffVersion 是一个带生效时刻的电价表版本。
type TariffVersion struct {
	EffectiveAt time.Time
	// Schedules 按 DayType 的取值索引，三种日类型都必须合法。
	Schedules [3]DaySchedule
}

// Reading 是某一时刻的累计电量读数（Wh，单调不减）。
type Reading struct {
	At time.Time
	Wh int64
}

// BillEntry 按 (日类型, 时段, 单价) 列示某月电量与金额。
type BillEntry struct {
	Day         DayType
	StartSec    int
	Price       int64
	EnergyWh    int64
	AmountMilli int64
}

// Bill 是某供电点某月的账单。
type Bill struct {
	Point      string
	MonthStart time.Time
	Closed     bool
	Entries    []BillEntry
	TotalWh    int64
	// TotalAmountMilli 为最小货币单位计的金额。
	TotalAmountMilli int64
	// UnbillableWh 为落在无任何电价版本可用时刻的电量。
	UnbillableWh int64
}

const (
	secondsPerDay       = int64(24 * 60 * 60)
	priceScale    int64 = 1000
)

// monthKey 以年*12+(月-1) 标识月份。
type monthKey int64

func monthKeyAt(t time.Time) monthKey {
	y, m, _ := t.Date()
	return monthKey(int64(y)*12 + int64(m) - 1)
}

func (k monthKey) start(loc *time.Location) time.Time {
	return time.Date(int(k/12), time.Month(k%12+1), 1, 0, 0, 0, 0, loc)
}

func (k monthKey) end(loc *time.Location) time.Time {
	return (k + 1).start(loc)
}

func (k monthKey) contains(t time.Time) bool {
	return monthKeyAt(t) == k
}

// validMoment 限定秒级精度与合理年份范围，保证分摊与取整可精确复现。
func validMoment(t time.Time) bool {
	if t.IsZero() || t.Nanosecond() != 0 {
		return false
	}
	y := t.Year()
	return y >= 2000 && y <= 2100
}

func validEnergy(wh int64) bool { return wh >= 0 }

func validPoint(p string) bool { return p != "" }
