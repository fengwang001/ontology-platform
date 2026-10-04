// Package wake 计算低功耗设备的接收窗口并处理参数重配。
package wake

import "errors"

// ErrInvalid 表示参数非法。
var ErrInvalid = errors.New("wake: invalid parameters")

// Params 描述设备的周期性接收窗口：[o+kP, o+kP+w)，k≥0。
type Params struct {
	P int64
	O int64
	W int64
}

// Device 保存一台设备最多两代窗口参数。
type Device struct {
	cur  params
	prev *oldWin
}

type params struct {
	p   Params
	eff int64
}

// oldWin 是换参当窗仍有效的旧窗口 [start,end)。
type oldWin struct {
	start int64
	end   int64
}

// New 创建设备，初始参数自时间零点起有效。
func New(p Params) *Device {
	return &Device{cur: params{p: p, eff: 0}}
}

// Reconfigure 在 now 换参。
func (d *Device) Reconfigure(p Params, now int64) error {
	if !p.valid() {
		return ErrInvalid
	}
	// now 落在某个仍有效窗口内时，该窗口按旧参数走完。
	eff := now
	if start, ok := d.WindowAt(now); ok {
		end := start + d.windowLen(start)
		d.prev = &oldWin{start: start, end: end}
		eff = end
	}
	d.cur = params{p: p, eff: eff}
	return nil
}

// WindowAt 返回 now 所在窗口的起点；不在任何窗口内时 ok 为 false。
func (d *Device) WindowAt(now int64) (int64, bool) {
	if d.prev != nil {
		if now >= d.prev.start && now < d.prev.end {
			return d.prev.start, true
		}
		// prev 已过期：可能回退到 cur（now 同时落在 cur 窗口的场景
		// 不存在——cur 自 eff=end 起），直接查 cur 即可，但不删除 prev。
	}
	s, ok := d.cur.windowAt(now)
	return s, ok
}

// NextAvail 返回下一个可用时刻：t 落在窗口内时即 t，否则为其后第一个
// 窗口的起点。
func (d *Device) NextAvail(t int64) int64 {
	if d.prev != nil {
		if t >= d.prev.start && t < d.prev.end {
			return t
		}
		if t < d.prev.start {
			return d.prev.start
		}
		// t≥end：cur 已经或即将接管。
	}
	if _, ok := d.cur.windowAt(t); ok {
		return t
	}
	return firstStart(d.cur.p, t)
}

// firstStart 返回参数自 eff 起第一个窗口的起点（起点≥eff）。
func firstStart(p Params, eff int64) int64 {
	if eff <= p.O {
		return p.O
	}
	k := (eff - p.O + p.P - 1) / p.P
	return p.O + k*p.P
}

func (p Params) valid() bool {
	return p.P >= 1 && p.P <= 1e9 &&
		p.O >= 0 && p.O < p.P &&
		p.W >= 1 && p.W <= p.P
}

// windowLen 返回当前两代参数中 start 所在窗口的长度。
func (d *Device) windowLen(start int64) int64 {
	if d.prev != nil && start == d.prev.start {
		return d.prev.end - d.prev.start
	}
	return d.cur.p.W
}

// windowAt 与单代参数模型一致：窗口 [s, s+W)。
func (cp params) windowAt(now int64) (int64, bool) {
	p := cp.p
	if now < p.O {
		return 0, false
	}
	s := p.O + ((now-p.O)/p.P)*p.P
	if s < cp.eff {
		return 0, false
	}
	if now < s+p.W {
		return s, true
	}
	return 0, false
}
