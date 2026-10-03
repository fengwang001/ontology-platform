// Package cal 描述工作日历并提供工作分钟数计算与反查。
package cal

import (
	"errors"
	"sync"
)

// 哨兵错误：用 errors.Is 区分拒绝类别。
var (
	// ErrArgument 参数非法（open/close/mask/day 越界、t1/t2 越界等）。
	ErrArgument = errors.New("cal: invalid argument")
	// ErrClock now 小于全局时钟。
	ErrClock = errors.New("cal: clock moved backwards")
	// ErrPast 节假日日号不在严格未来。
	ErrPast = errors.New("cal: holiday day is not strictly in the future")
)

const (
	minutesPerDay = 1440
	// MaxTime 时间轴上界。
	MaxTime = int64(1_000_000_000_000)
	// MaxDay 日号上界（6.9×10^8）。
	MaxDay = int64(690_000_000)
)

// Calendar 工作日历：每个工作日的工作窗口为 [day*1440+open, day*1440+close)，
// mask 的第 w 位为 1 表示星期 w 工作；holidays 中的日号整日不工作。
type Calendar struct {
	mu       sync.RWMutex
	open     int64
	close    int64
	mask     uint8
	winLen   int64
	perWeek  int64
	clock    int64
	holidays *holidaySet
}

// New 构造工作日历并校验参数。
func New(open, close int64, mask uint8) (*Calendar, error) {
	if open < 0 || open >= minutesPerDay || close <= 0 || close > minutesPerDay ||
		open >= close || mask < 1 || mask > 127 {
		return nil, ErrArgument
	}
	c := &Calendar{
		open:     open,
		close:    close,
		mask:     mask,
		winLen:   close - open,
		holidays: newHolidaySet(),
	}
	for w := uint(0); w < 7; w++ {
		if mask&(1<<w) != 0 {
			c.perWeek += c.winLen
		}
	}
	return c, nil
}

// AddHoliday 添加严格未来的节假日；重复添加为成功的无操作。
func (c *Calendar) AddHoliday(day, now int64) error {
	if day < 0 || day > MaxDay || now < 0 || now > MaxTime {
		return ErrArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.clock {
		return ErrClock
	}
	if c.holidays.contains(day) {
		c.clock = now
		return nil
	}
	if day <= now/minutesPerDay {
		return ErrPast
	}
	c.holidays.insert(day)
	c.clock = now
	return nil
}

// Work 返回区间 [t1,t2) 内的工作分钟数。
func (c *Calendar) Work(t1, t2 int64) (int64, error) {
	if t1 < 0 || t2 < t1 || t2 > MaxTime {
		return 0, ErrArgument
	}
	if t1 == t2 {
		return 0, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prefix(t2) - c.prefix(t1), nil
}

// prefix 返回 [0,t) 内的工作分钟数（整周批量 + 节假日修正 + 当日零头）。
func (c *Calendar) prefix(t int64) int64 {
	day, rem := t/minutesPerDay, t%minutesPerDay
	fullWeeks, restDays := day/7, day%7
	work := fullWeeks * c.perWeek
	for w := int64(0); w < restDays; w++ {
		if c.mask&(1<<uint(w)) != 0 {
			work += c.winLen
		}
	}
	work -= c.holidays.countWorkdayBelow(day, c.mask) * c.winLen
	if rem > c.open && c.isWorkday(day) {
		part := rem - c.open
		if part > c.winLen {
			part = c.winLen
		}
		work += part
	}
	return work
}

// CheckClock 仅校验 now 合法性与全局时钟单调性，不推进时钟。
// 上层包应：先 CheckClock，再完成自身状态变更，成功后调用 AdvanceClock。
func (c *Calendar) CheckClock(now int64) error {
	if now < 0 || now > MaxTime {
		return ErrArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.clock {
		return ErrClock
	}
	return nil
}

// AdvanceClock 在操作成功后推进全局时钟（now 已由 CheckClock 校验）。
func (c *Calendar) AdvanceClock(now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now > c.clock {
		c.clock = now
	}
}

// Advance 为 Advance 的导出版：从 t 起消耗 need 个工作分钟后的时刻。
func (c *Calendar) Advance(t, need int64) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.advance(t, need)
}
