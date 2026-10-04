package guard

// naiveGuard 是与实现相互独立的逐秒朴素模拟器：
// 每秒先结算在线并集 1 秒，再处理取等超时、宵禁与额度强制下线。
type naiveGuard struct {
	tz, cs, ce, lw, lh, hb int64
	dmax                   int
	now                    int64
	registered             bool
	devices                map[string]int64 // dev -> last
	used                   map[int64]int64
	holidays               map[int64]bool
}

func newNaive(tz, cs, ce, lw, lh, hb int64, dmax int) *naiveGuard {
	return &naiveGuard{
		tz: tz, cs: cs, ce: ce, lw: lw, lh: lh, hb: hb, dmax: dmax,
		devices: map[string]int64{}, used: map[int64]int64{}, holidays: map[int64]bool{},
	}
}

func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func (n *naiveGuard) dayX(t int64) (int64, int64) {
	s := t + n.tz
	d := floorDiv(s, 86400)
	return d, s - d*86400
}

func (n *naiveGuard) curfewX(x int64) bool {
	switch {
	case n.cs == n.ce:
		return false
	case n.cs < n.ce:
		return n.cs <= x && x < n.ce
	default:
		return x >= n.cs || x < n.ce
	}
}

func (n *naiveGuard) curfew(t int64) bool {
	_, x := n.dayX(t)
	return n.curfewX(x)
}

func (n *naiveGuard) limit(day int64) int64 {
	if n.holidays[day] {
		return n.lh
	}
	return n.lw
}

type naiveSnap struct {
	now     int64
	devices map[string]int64
	used    map[int64]int64
}

func (n *naiveGuard) take() naiveSnap {
	devs := map[string]int64{}
	for k, v := range n.devices {
		devs[k] = v
	}
	us := map[int64]int64{}
	for k, v := range n.used {
		us[k] = v
	}
	return naiveSnap{now: n.now, devices: devs, used: us}
}

func (n *naiveGuard) rollback(s naiveSnap) {
	n.now = s.now
	n.devices = s.devices
	n.used = s.used
}

// advance 逐秒推进到 target：区间 [now,target) 每秒先记账，再处理超时与强制下线。
func (n *naiveGuard) advance(target int64) {
	for n.now < target && len(n.devices) > 0 {
		sec := n.now
		day, x := n.dayX(sec)
		// 秒 [sec,sec+1)：仅当非宵禁且当日有剩余额度时入账。
		if !n.curfewX(x) && n.used[day] < n.limit(day) {
			n.used[day]++
		}
		n.now = sec + 1
		// 取等超时：到达 last+HB 离线。
		for dev, last := range n.devices {
			if n.now >= last+n.hb {
				delete(n.devices, dev)
			}
		}
		// 强制下线：进入宵禁，或当日已用达到额度。
		if len(n.devices) > 0 {
			d, _ := n.dayX(n.now)
			if n.curfew(n.now) || n.used[d] >= n.limit(d) {
				n.devices = map[string]int64{}
			}
		}
	}
	if n.now < target {
		n.now = target
	}
}

func (n *naiveGuard) valid(now int64, dev string) bool {
	return now >= 0 && now <= 1e10 && dev != ""
}

// login 按常规次序（参数>时钟>账号）处理登录。
func (n *naiveGuard) loginAcct(now int64, acct, dev string) error {
	if !n.valid(now, dev) {
		return ErrInvalidParam
	}
	if now < n.now {
		return ErrClockBackward
	}
	if acct != "a" || !n.registered {
		return ErrNoAccount
	}
	snap := n.take()
	n.advance(now)
	if n.curfew(now) {
		n.rollback(snap)
		return ErrInCurfew
	}
	day, _ := n.dayX(now)
	if n.used[day] >= n.limit(day) {
		n.rollback(snap)
		return ErrQuotaExhausted
	}
	if _, ok := n.devices[dev]; ok {
		n.rollback(snap)
		return ErrDeviceOnline
	}
	if len(n.devices) >= n.dmax {
		n.rollback(snap)
		return ErrTooManyDevice
	}
	n.devices[dev] = now
	return nil
}

func (n *naiveGuard) login(now int64, dev string) error {
	return n.loginAcct(now, "a", dev)
}

func (n *naiveGuard) heartbeat(now int64, dev string) error {
	if !n.valid(now, dev) {
		return ErrInvalidParam
	}
	if now < n.now {
		return ErrClockBackward
	}
	if !n.registered {
		return ErrNoAccount
	}
	snap := n.take()
	n.advance(now)
	if _, ok := n.devices[dev]; !ok {
		n.rollback(snap)
		return ErrDeviceOffline
	}
	n.devices[dev] = now
	return nil
}

func (n *naiveGuard) logout(now int64, dev string) error {
	if !n.valid(now, dev) {
		return ErrInvalidParam
	}
	if now < n.now {
		return ErrClockBackward
	}
	if !n.registered {
		return ErrNoAccount
	}
	snap := n.take()
	n.advance(now)
	if _, ok := n.devices[dev]; !ok {
		n.rollback(snap)
		return ErrDeviceOffline
	}
	delete(n.devices, dev)
	return nil
}

func (n *naiveGuard) remaining(now int64) (int64, error) {
	if now < 0 || now > 1e10 {
		return 0, ErrInvalidParam
	}
	if now < n.now {
		return 0, ErrClockBackward
	}
	if !n.registered {
		return 0, ErrNoAccount
	}
	snap := n.take()
	n.advance(now)
	day, _ := n.dayX(now)
	v := n.limit(day) - n.used[day]
	n.rollback(snap)
	return v, nil
}

func (n *naiveGuard) usedAt(now, day int64) (int64, error) {
	if now < 0 || now > 1e10 {
		return 0, ErrInvalidParam
	}
	if now < n.now {
		return 0, ErrClockBackward
	}
	if !n.registered {
		return 0, ErrNoAccount
	}
	snap := n.take()
	n.advance(now)
	v := n.used[day]
	n.rollback(snap)
	return v, nil
}

func (n *naiveGuard) setHoliday(now, day int64, on bool) error {
	if now < 0 || now > 1e10 {
		return ErrInvalidParam
	}
	if now < n.now {
		return ErrClockBackward
	}
	snap := n.take()
	n.advance(now)
	d0, _ := n.dayX(now)
	if day <= d0 {
		n.rollback(snap)
		return ErrTooLate
	}
	if on {
		n.holidays[day] = true
	} else {
		delete(n.holidays, day)
	}
	return nil
}
