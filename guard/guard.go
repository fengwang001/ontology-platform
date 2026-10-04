package guard

import (
	"errors"
	"sort"
	"sync"

	"ontology/calendar"
	"ontology/quota"
)

var (
	ErrInvalidParam   = errors.New("guard: invalid parameter")
	ErrClockBack      = errors.New("guard: clock moved backwards")
	ErrNoAccount      = errors.New("guard: account not registered")
	ErrCurfew         = errors.New("guard: curfew active")
	ErrQuotaExhausted = errors.New("guard: daily quota exhausted")
	ErrDeviceOnline   = errors.New("guard: device already online")
	ErrDeviceLimit    = errors.New("guard: online device limit reached")
	ErrDeviceOffline  = errors.New("guard: device not online")
	ErrTooLate        = errors.New("guard: holiday set too late")
)

type Guard struct {
	mu     sync.Mutex
	cal    *calendar.Cal
	lw, lh int64
	hb     int64
	dmax   int
	maxN   int64
	accts  map[string]*account
}

// device: key 为设备 id，value 为最后活动时刻 last。
type account struct {
	devs    map[string]int64
	settled int64
	steps   int
	qm      *quota.Manager
}

func New(tz, cs, ce, lw, lh, hb int64, dmax int) (*Guard, error) {
	cal, err := calendar.New(tz, cs, ce)
	if err != nil {
		return nil, ErrInvalidParam
	}
	// 用一次构造校验额度参数；真正的 Manager 在 Register 时按账号创建。
	if lw < 0 || lw > 86400 || lh < 0 || lh > 86400 {
		return nil, ErrInvalidParam
	}
	if hb < 1 || hb > 3600 || dmax < 1 || dmax > 8 {
		return nil, ErrInvalidParam
	}
	return &Guard{cal: cal, lw: lw, lh: lh, hb: hb, dmax: dmax, accts: map[string]*account{}}, nil
}

// advance 必须持有 g.mu。把账号从 a.settled 按事件推进到 now。
// 无设备在线时直接跳到 now，steps 不增加。
func (g *Guard) advance(a *account, now int64) {
	if now < a.settled {
		return
	}
	if len(a.devs) == 0 {
		a.settled = now
		return
	}
	t := a.settled
	for t < now && len(a.devs) > 0 {
		forceOffline := false
		dayAtStart := g.cal.Day(t)
		inCurfew := g.cal.CurfewAt(t)
		next := now
		// 事件 1：最早的设备心跳超时（取等超时）。
		earliestTO := int64(0)
		for _, last := range a.devs {
			to := last + g.hb
			if earliestTO == 0 || to < earliestTO {
				earliestTO = to
			}
		}
		if earliestTO < next {
			next = earliestTO
		}
		// 事件 2：日界切换。
		dayStart := g.cal.DayStart(dayAtStart)
		dayEnd := dayStart + 86400
		if dayEnd < next {
			next = dayEnd
		}
		// 事件 3：宵禁开始（仅当 t 不在宵禁中）。
		if !inCurfew {
			if cs, ok := g.cal.NextCurfewStart(t); ok && cs < next {
				next = cs
			}
		}
		// 事件 4：当日剩余额度耗尽（仅当 t 非宵禁）。
		exhaustAt := int64(-1)
		if !inCurfew {
			rem := a.qm.Remaining(t)
			if rem == 0 {
				exhaustAt = -1 // 当日已尽只可能伴随日界事件处理
			} else {
				exhaustAt = t + rem
				if exhaustAt < next {
					next = exhaustAt
				}
			}
		}
		if exhaustAt >= 0 && exhaustAt == next {
			forceOffline = true
		}
		a.steps++
		// 结算 (t, next) 的在线计时：宵禁段不增长；否则按本地日界切分入账。
		if next > t && !inCurfew {
			segStart := t
			for segStart < next {
				d := g.cal.Day(segStart)
				segEnd := g.cal.DayStart(d) + 86400
				if segEnd > next {
					segEnd = next
				}
				_ = a.qm.Add(d, segEnd-segStart)
				segStart = segEnd
			}
		}
		t = next
		// 超时离线。
		for dev, last := range a.devs {
			if last+g.hb <= t {
				delete(a.devs, dev)
			}
		}
		// 强制下线判定（按规格的两种触发）：
		// （一）在旧日耗尽（含恰好日末）——必须先于日界重置检查；
		// （二）进入宵禁；日界后若新日额度为 0 同样在此分支处理。
		crossedDay := g.cal.Day(t) != dayAtStart
		crossedToZeroDay := crossedDay && a.qm.Remaining(t) <= 0
		if g.cal.CurfewAt(t) || forceOffline || crossedToZeroDay {
			a.devs = map[string]int64{}
		}
	}
	// 设备在推进途中全部离线（超时/强制下线）后，剩余区间无计时，
	// 结算点直接跳到 now，保证后续同刻操作不重复处理历史事件。
	if len(a.devs) == 0 && t < now {
		t = now
	}
	a.settled = t
}

func (g *Guard) checkClock(now int64) error {
	if now < 0 || now > 10_000_000_000 {
		return ErrInvalidParam
	}
	if now < g.maxN {
		return ErrClockBack
	}
	return nil
}

// Register 登记账号；重复登记幂等，不改变任何既有状态。
func (g *Guard) Register(acct string) error {
	if acct == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.accts[acct]; !ok {
		qm, _ := quota.New(g.cal, g.lw, g.lh)
		g.accts[acct] = &account{devs: map[string]int64{}, qm: qm}
	}
	return nil
}

func (g *Guard) Login(now int64, acct, dev string) error {
	if acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	g.advance(a, now)
	g.maxN = now
	if g.cal.CurfewAt(now) {
		return ErrCurfew
	}
	if a.qm.Remaining(now) <= 0 {
		return ErrQuotaExhausted
	}
	if _, online := a.devs[dev]; online {
		return ErrDeviceOnline
	}
	if len(a.devs) >= g.dmax {
		return ErrDeviceLimit
	}
	a.devs[dev] = now
	return nil
}

func (g *Guard) Heartbeat(now int64, acct, dev string) error {
	if acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	g.advance(a, now)
	g.maxN = now
	if _, online := a.devs[dev]; !online {
		return ErrDeviceOffline
	}
	a.devs[dev] = now
	return nil
}

func (g *Guard) Logout(now int64, acct, dev string) error {
	if acct == "" || dev == "" {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	a, ok := g.accts[acct]
	if !ok {
		return ErrNoAccount
	}
	g.advance(a, now)
	g.maxN = now
	if _, online := a.devs[dev]; !online {
		return ErrDeviceOffline
	}
	delete(a.devs, dev)
	return nil
}

func (g *Guard) Remaining(now int64, acct string) (int64, error) {
	if acct == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return 0, err
	}
	a, ok := g.accts[acct]
	if !ok {
		return 0, ErrNoAccount
	}
	g.advance(a, now)
	g.maxN = now
	return a.qm.Remaining(now), nil
}

func (g *Guard) Used(now int64, acct string, day int64) (int64, error) {
	if acct == "" {
		return 0, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return 0, err
	}
	a, ok := g.accts[acct]
	if !ok {
		return 0, ErrNoAccount
	}
	g.advance(a, now)
	g.maxN = now
	return a.qm.Used(day), nil
}

func (g *Guard) SetHoliday(now, day int64, on bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	g.maxN = now
	if err := g.cal.SetHoliday(now, day, on); err != nil {
		if errors.Is(err, calendar.ErrTooLate) {
			return ErrTooLate
		}
		return ErrInvalidParam
	}
	return nil
}

// Steps 返回账号入口推进自登记以来累计的事件步数（非导出计数器，供测试验证）。
func (g *Guard) Steps(acct string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if a, ok := g.accts[acct]; ok {
		return a.steps
	}
	return -1
}

// onlineDevices 返回推进到 now 后仍在线的设备（测试辅助）。
func (g *Guard) onlineDevices(now int64, acct string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.accts[acct]
	if !ok {
		return nil
	}
	g.advance(a, now)
	g.maxN = now
	out := make([]string, 0, len(a.devs))
	for dev := range a.devs {
		out = append(out, dev)
	}
	sort.Strings(out)
	return out
}
