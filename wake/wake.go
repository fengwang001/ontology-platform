// Package wake 计算低功耗休眠设备的接收窗口并支持换参过渡期。
package wake

// Params 描述一组接收窗口参数：窗口为 [o+kP, o+kP+w)。
type Params struct {
	P int64
	O int64
	W int64
}

// Device 记录设备当前（可能处于过渡期的）窗口参数。
type Device struct {
	active   Params
	graceEnd int64 // 过渡期结束时刻 e；now<e 时仍走旧参数
	pending  Params
	hasPend  bool
}

// New 以初始参数创建设备。
func New(p Params) *Device { return &Device{active: p, graceEnd: -1} }

// FirstStart 返回不早于 from 的第一个窗口起点（起点恰等于 from 算在内）。
func FirstStart(p Params, from int64) int64 {
	if from <= p.O {
		return p.O
	}
	k := (from - p.O + p.P - 1) / p.P
	return p.O + k*p.P
}

// effective 把已到期的换参落到 active 上，并返回 now 时刻真正生效的参数。
func (d *Device) effective(now int64) Params {
	if d.hasPend && now >= d.graceEnd {
		d.active = d.pending
		d.hasPend = false
		d.graceEnd = -1
	}
	return d.active
}

// Effective 返回 now 时刻生效的参数（过渡期内返回旧参数）。
func (d *Device) Effective(now int64) Params { return d.effective(now) }

// WindowStart 返回 now 所在窗口的起点；now 不在任何窗口内时第二返回值为 false。
func (d *Device) WindowStart(now int64) (int64, bool) {
	p := d.effective(now)
	if now < p.O {
		return 0, false
	}
	k := (now - p.O) / p.P
	s := p.O + k*p.P
	if now < s+p.W {
		return s, true
	}
	return 0, false
}

// NextAvailable 返回下一可用时刻：now 在窗口内取 now，否则取其后首个窗口起点。
func (d *Device) NextAvailable(now int64) int64 {
	if s, ok := d.WindowStart(now); ok {
		_ = s
		return now
	}
	return FirstStart(d.Effective(now), now)
}

// Reconfigure 换参，返回新参数生效时刻 e。
func (d *Device) Reconfigure(p Params, now int64) int64 {
	old := d.effective(now)
	e := now
	if now >= old.O {
		k := (now - old.O) / old.P
		s := old.O + k*old.P
		if now < s+old.W {
			e = s + old.W
		}
	}
	d.pending = p
	d.graceEnd = e
	d.hasPend = true
	return e
}
