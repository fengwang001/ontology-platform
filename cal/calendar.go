package cal

import "sync"

const (
	MinPerDay   = int64(1440)
	MaxTime     = int64(1_000_000_000_000)
	MaxHolidays = 100_000
)

// Calendar 描述统一的工作日历：每个掩码工作日开同一窗口 [open,close)，
// holidays 为整日不工作的日号有序切片。clock 为全局时钟（分钟）。
type Calendar struct {
	mu      sync.RWMutex
	open    int64
	close   int64
	mask    uint8
	holiday []int64
	clock   int64
}

func New(open, close int64, mask uint8) *Calendar {
	if open < 0 || open >= MinPerDay || close <= open || close > MinPerDay || mask < 1 || mask > 127 {
		return nil
	}
	return &Calendar{open: open, close: close, mask: mask}
}

func (c *Calendar) AddHoliday(day, now int64) error {
	if day < 0 || day > MaxTime/MinPerDay || now < 0 || now > MaxTime {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.clock {
		return ErrClock
	}
	if day <= now/MinPerDay {
		return ErrPast
	}
	if len(c.holiday) >= MaxHolidays && !c.isHolidayLocked(day) {
		return ErrInvalid
	}
	if !c.isHolidayLocked(day) {
		c.holiday = append(c.holiday, day)
		for i := len(c.holiday) - 1; i > 0 && c.holiday[i-1] > c.holiday[i]; i-- {
			c.holiday[i-1], c.holiday[i] = c.holiday[i], c.holiday[i-1]
		}
	}
	c.clock = now
	return nil
}

func (c *Calendar) Work(t1, t2 int64) int64 {
	if t1 < 0 || t2 < t1 || t2 > MaxTime {
		return -1
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workLocked(t1, t2)
}

func (c *Calendar) Clock() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clock
}

// RLock/RUnlock 供 sla、alert 包在持日历读锁期间完成复合只读计算。
func (c *Calendar) RLock()   { c.mu.RLock() }
func (c *Calendar) RUnlock() { c.mu.RUnlock() }

func (c *Calendar) ClockLocked() int64 { return c.clock }

// CheckAndAdvance 校验 now 合法且不小于全局时钟，成功则推进。调用方不得持有 c 的锁。
func (c *Calendar) CheckAndAdvance(now int64) error {
	if now < 0 || now > MaxTime {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.clock {
		return ErrClock
	}
	c.clock = now
	return nil
}

// CheckClock 只读校验 now 合法且不小于全局时钟，不推进。调用方不得持有 c 的锁。
func (c *Calendar) CheckClock(now int64) error {
	if now < 0 || now > MaxTime {
		return ErrInvalid
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if now < c.clock {
		return ErrClock
	}
	return nil
}

// AdvanceClock 在其余拒绝项全部通过后提交推进（调用方持 m 锁）。
func (c *Calendar) AdvanceClock(now int64) {
	c.mu.Lock()
	if now > c.clock {
		c.clock = now
	}
	c.mu.Unlock()
}

func (c *Calendar) IsHolidayLocked(day int64) bool { return c.isHolidayLocked(day) }

func (c *Calendar) isHolidayLocked(day int64) bool {
	lo, hi := 0, len(c.holiday)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if c.holiday[mid] < day {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(c.holiday) && c.holiday[lo] == day
}
