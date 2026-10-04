// Package guard 组合 calendar 与 quota，管理多设备会话与强制下线。
package guard

import (
	"errors"
	"sync"

	"ontology/calendar"
	"ontology/quota"
)

var (
	ErrInvalidParam   = errors.New("guard: invalid parameter")
	ErrClockBackward  = errors.New("guard: clock moved backward")
	ErrNoAccount      = errors.New("guard: account not registered")
	ErrInCurfew       = errors.New("guard: curfew in effect")
	ErrQuotaExhausted = errors.New("guard: daily quota exhausted")
	ErrDeviceOnline   = errors.New("guard: device already online")
	ErrTooManyDevice  = errors.New("guard: online device limit reached")
	ErrDeviceOffline  = errors.New("guard: device not online")

	// ErrTooLate 透传 calendar 的“为时已晚”，便于调用方用 errors.Is 判定。
	ErrTooLate = calendar.ErrTooLate
)

type account struct {
	devices map[string]int64
	settled int64
	steps   int64
	used    *quota.Quota
}

// snapshot 捕获账号推进前的可变状态，便于被拒操作回滚。
type snapshot struct {
	devices map[string]int64
	settled int64
	used    map[int64]int64
}

func (a *account) take() snapshot {
	devs := make(map[string]int64, len(a.devices))
	for k, v := range a.devices {
		devs[k] = v
	}
	return snapshot{devices: devs, settled: a.settled, used: a.used.Snapshot()}
}

func (a *account) restore(s snapshot) {
	a.devices = s.devices
	a.settled = s.settled
	a.used.Restore(s.used)
}

// Guard 是防沉迷管控器。
type Guard struct {
	cal *calendar.Calendar

	lw, lh int64
	hb     int64
	dmax   int
	maxNow int64
	accts  map[string]*account

	mu sync.Mutex
}

// New 创建管控器，时间单位均为秒。
func New(tz, cs, ce, lw, lh, hb int64, dmax int) *Guard {
	if tz < -43200 || tz > 50400 || cs < 0 || cs > 86399 || ce < 0 || ce > 86399 ||
		lw < 0 || lw > 86400 || lh < 0 || lh > 86400 ||
		hb < 1 || hb > 3600 || dmax < 1 || dmax > 8 {
		return nil
	}
	return &Guard{
		cal:   calendar.New(tz, cs, ce),
		lw:    lw,
		lh:    lh,
		hb:    hb,
		dmax:  dmax,
		accts: map[string]*account{},
	}
}

// Calendar 暴露日历以便设置节假日。
func (g *Guard) Calendar() *calendar.Calendar { return g.cal }

// SetHoliday 经统一时钟检查后设置节假日。
func (g *Guard) SetHoliday(now, day int64, on bool) error {
	if now < 0 || now > 1e10 {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return ErrClockBackward
	}
	snaps := map[*account]snapshot{}
	for _, a := range g.accts {
		snaps[a] = a.take()
	}
	prevClock := g.maxNow
	for _, a := range g.accts {
		g.advance(a, now)
	}
	if err := g.cal.SetHoliday(now, day, on); err != nil {
		for _, a := range g.accts {
			a.restore(snaps[a])
		}
		g.maxNow = prevClock
		return err
	}
	g.maxNow = now
	return nil
}

// Register 登记账号。
func (g *Guard) Register(acct string) error {
	if acct == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.accts[acct]; !ok {
		g.accts[acct] = &account{devices: map[string]int64{}, used: quota.New()}
	}
	return nil
}

// Login 使设备上线。
func (g *Guard) Login(now int64, acct, dev string) error {
	if now < 0 || now > 1e10 || acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return ErrClockBackward
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	prevClock := g.maxNow
	snap := a.take()
	g.advance(a, now)
	if g.cal.InCurfew(now) {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrInCurfew
	}
	day, _ := g.cal.Local(now)
	if a.used.Used(day) >= g.limit(day) {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrQuotaExhausted
	}
	if _, online := a.devices[dev]; online {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrDeviceOnline
	}
	if len(a.devices) >= g.dmax {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrTooManyDevice
	}
	a.devices[dev] = now
	g.maxNow = now
	return nil
}

// Heartbeat 续约设备。
func (g *Guard) Heartbeat(now int64, acct, dev string) error {
	if now < 0 || now > 1e10 || acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return ErrClockBackward
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	prevClock := g.maxNow
	snap := a.take()
	g.advance(a, now)
	if _, online := a.devices[dev]; !online {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrDeviceOffline
	}
	a.devices[dev] = now
	g.maxNow = now
	return nil
}

// Logout 使设备下线。
func (g *Guard) Logout(now int64, acct, dev string) error {
	if now < 0 || now > 1e10 || acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return ErrClockBackward
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	prevClock := g.maxNow
	snap := a.take()
	g.advance(a, now)
	if _, online := a.devices[dev]; !online {
		a.restore(snap)
		g.maxNow = prevClock
		return ErrDeviceOffline
	}
	delete(a.devices, dev)
	g.maxNow = now
	return nil
}

// Remaining 返回账号当日剩余额度。
func (g *Guard) Remaining(now int64, acct string) (int64, error) {
	if now < 0 || now > 1e10 || acct == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return 0, ErrClockBackward
	}
	a, ok := g.accts[acct]
	if !ok {
		return 0, ErrNoAccount
	}
	snap := a.take()
	g.advance(a, now)
	day, _ := g.cal.Local(now)
	rem := g.limit(day) - a.used.Used(day)
	a.restore(snap)
	return rem, nil
}

// Used 返回账号某日已用。
func (g *Guard) Used(now int64, acct string, day int64) (int64, error) {
	if now < 0 || now > 1e10 || acct == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.maxNow {
		return 0, ErrClockBackward
	}
	a, ok := g.accts[acct]
	if !ok {
		return 0, ErrNoAccount
	}
	snap := a.take()
	g.advance(a, now)
	v := a.used.Used(day)
	a.restore(snap)
	return v, nil
}

// Steps 返回该账号最近一次入口推进消耗的事件步数。
func (g *Guard) Steps(acct string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.accts[acct]
	if !ok {
		return 0
	}
	return a.steps
}

func (g *Guard) limit(day int64) int64 {
	if g.cal.IsHoliday(day) {
		return g.lh
	}
	return g.lw
}

// accrue 把 [from,to) 的在线时长按本地日切分入账；返回入账总额。
func (g *Guard) accrue(a *account, from, to int64) int64 {
	total := int64(0)
	t := from
	for t < to {
		day, _ := g.cal.Local(t)
		end := to
		if dayEnd := g.cal.DayStart(day) + 86400; dayEnd < end {
			end = dayEnd
		}
		seg := end - t
		total += a.used.Add(day, seg, g.limit(day))
		t = end
	}
	return total
}

// advance 把账号从上次结算时刻按事件推进到 now。
// 依次处理设备超时、日界、额度耗尽、进入宵禁；到达强制下线点时全部设备离线。
func (g *Guard) advance(a *account, now int64) {
	a.steps = 0
	for len(a.devices) > 0 && a.settled < now {
		a.steps++

		day, _ := g.cal.Local(a.settled)
		tDay := g.cal.DayStart(day + 1)

		// 候选事件点：下一个设备超时、日界、当日额度耗尽、进入宵禁。
		events := []int64{now}
		for _, last := range a.devices {
			if to := last + g.hb; to > a.settled {
				events = append(events, to)
			}
		}
		if tDay > a.settled {
			events = append(events, tDay)
		}
		tExhaust := int64(-1)
		if rem := g.limit(day) - a.used.Used(day); rem > 0 {
			// 耗尽点可恰为日界（恰在日末用满当日额度）。
			if te := a.settled + rem; te <= tDay && te > a.settled {
				events = append(events, te)
				tExhaust = te
			}
		}
		tCurfew := g.cal.NextCurfewStart(a.settled)
		if tCurfew > a.settled {
			events = append(events, tCurfew)
		}

		t := events[0]
		for _, e := range events[1:] {
			if e < t {
				t = e
			}
		}

		// 结算到事件点（按日切分在 accrue 内完成）。
		if t > a.settled {
			g.accrue(a, a.settled, t)
			a.settled = t
		}

		// 强制下线：进入宵禁那一刻，或当日额度恰好用尽那一刻（含日末用尽、
		// 跨入额度为 0 的新日）。日界本身只做切分，不触发下线。
		force := tCurfew >= 0 && t == tCurfew
		if !force {
			if tExhaust >= 0 && t == tExhaust {
				force = true
			} else if t == tDay {
				if nd, _ := g.cal.Local(t); g.limit(nd) == 0 {
					force = true
				}
			}
		}
		if force {
			a.devices = map[string]int64{}
			break
		}

		// 设备取等超时：删除所有 last+HB<=t 的设备。
		for dev, last := range a.devices {
			if last+g.hb <= t {
				delete(a.devices, dev)
			}
		}
	}
	if a.settled < now {
		a.settled = now
	}
}
