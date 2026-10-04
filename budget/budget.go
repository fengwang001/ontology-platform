// Package budget 累计冷链货物单元的加权超温暴露并判定判废时刻。
package budget

import (
	"sync"

	"ontology/probe"
)

const maxNow = 1_000_000_000

// span 为一段左闭右开的装载或空档区间。
// 装载段的权重不缓存：设备读数可能晚于装载到达，评估时实时查 probe。
type span struct {
	start    int64
	end      int64 // 0 表示开放段
	onDevice bool
	device   string
}

type unit struct {
	regAt     int64
	onDevice  bool
	device    string
	lastAt    int64
	spoiledAt int64
	released  bool
	frozenE   int64
	frozenAt  int64
	spans     []span
	open      span
}

// Tracker 维护全部单元与暴露预算。
type Tracker struct {
	mu    sync.Mutex
	p     *probe.Store
	h     int64
	bmax  int64
	units map[string]*unit
	clock bool
}

// New 构造预算跟踪器。
func New(p *probe.Store, grace, bmax int64) *Tracker {
	if grace < 1 || grace > 1_000_000 || bmax < 1 || bmax > 1_000_000 {
		panic(probe.ErrInvalid)
	}
	return &Tracker{p: p, h: grace, bmax: bmax, units: make(map[string]*unit), clock: true}
}

// DisableClock 由 release 层统一管理时钟时调用。
func (t *Tracker) DisableClock() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clock = false
}

func (t *Tracker) checkClock(now int64) error {
	if t.clock {
		return t.p.CheckClock(now)
	}
	return nil
}

// EvalResult 为一次评估的结果。
type EvalResult struct {
	E         int64
	Spoiled   bool
	SpoiledAt int64
}

// spanWeight 返回 sp 内 [lo,hi) 的暴露增量。装载段实时查 probe。
func (t *Tracker) spanWeight(sp span, lo, hi int64) int64 {
	if hi <= lo {
		return 0
	}
	if sp.onDevice {
		sum, ok := t.p.Weight(sp.device, lo, hi)
		if !ok {
			return 0
		}
		return sum
	}
	l := hi - lo
	light := l
	if light > t.h {
		light = t.h
	}
	return light + (l-light)*t.p.HeavyWeight()
}

// crossSpan 返回在 sp 内使“自 startE 起累计”首次 > bmax 的分钟；无则 0。
func (t *Tracker) crossSpan(sp span, startE int64) int64 {
	if startE > t.bmax {
		return sp.start
	}
	need := t.bmax + 1 - startE
	if sp.onDevice {
		if tt, ok := t.p.CrossAfter(sp.device, sp.start, need, sp.end); ok {
			return tt
		}
		return 0
	}
	l := sp.end - sp.start
	light := l
	if light > t.h {
		light = t.h
	}
	if need <= light {
		return sp.start + need
	}
	remain := need - light
	heavy := l - light
	if remain <= heavy*t.p.HeavyWeight() {
		m := (remain + t.p.HeavyWeight() - 1) / t.p.HeavyWeight()
		return sp.start + light + m
	}
	return 0
}

// allSpans 返回闭合 span 加上到 x 的开放视图。
func (t *Tracker) viewSpans(u *unit, x int64) []span {
	out := append([]span(nil), u.spans...)
	if x > u.lastAt {
		op := u.open
		op.end = x
		out = append(out, op)
	}
	return out
}

// exposureAt 实时累计 E(unit,x)。
func (t *Tracker) exposureAt(u *unit, x int64) int64 {
	if u.released && x >= u.frozenAt {
		return u.frozenE
	}
	var sum int64
	spans := t.viewSpans(u, x)
	for _, sp := range spans {
		sum += t.spanWeight(sp, sp.start, sp.end)
	}
	return sum
}

// resolveSpoil 自登记起扫描全部 span（装载段实时查 probe），定位判废分钟。
// 采用“按 span 前缀二分跳过已确认不超标部分”：这里 span 数即操作数，
// 顺序扫描 span 不会触碰 probe 读数记录（CrossAfter 内部二分）。
func (t *Tracker) resolveSpoil(u *unit, x int64) {
	if u.spoiledAt != 0 {
		return
	}
	if u.released {
		// 放行前必须未判废；冻结时不再改变。
		return
	}
	var sum int64
	for _, sp := range t.viewSpans(u, x) {
		tt := t.crossSpan(sp, sum)
		if tt != 0 {
			u.spoiledAt = tt
			return
		}
		sum += t.spanWeight(sp, sp.start, sp.end)
	}
}

// closeOpen 在 now 闭合当前开放段并开始新段（由调用方设置 on/device）。
func (t *Tracker) closeOpen(u *unit, now int64) {
	if now == u.lastAt {
		return
	}
	u.open.end = now
	u.spans = append(u.spans, u.open)
	u.lastAt = now
}

// Register 登记单元并装到设备上。
func (t *Tracker) Register(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > maxNow {
		return probe.ErrInvalid
	}
	if err := t.checkClock(now); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if u := t.units[unitID]; u != nil {
		return probe.ErrConflict
	}
	if !t.p.HasDevice(device) {
		return probe.ErrState
	}
	u := &unit{
		regAt: now, onDevice: true, device: device, lastAt: now,
		open: span{start: now, onDevice: true, device: device},
	}
	t.units[unitID] = u
	return nil
}

// Unload 将单元从设备卸下（开始交接空档）。
func (t *Tracker) Unload(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > maxNow {
		return probe.ErrInvalid
	}
	if err := t.checkClock(now); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	if !u.onDevice || u.device != device {
		return probe.ErrState
	}
	t.closeOpen(u, now)
	u.onDevice = false
	u.device = ""
	u.open = span{start: now}
	return nil
}

// Load 将单元重新装到设备上。
func (t *Tracker) Load(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > maxNow {
		return probe.ErrInvalid
	}
	if err := t.checkClock(now); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	if u.onDevice {
		return probe.ErrState
	}
	if !t.p.HasDevice(device) {
		return probe.ErrState
	}
	t.closeOpen(u, now)
	u.onDevice = true
	u.device = device
	u.open = span{start: now, onDevice: true, device: device}
	return nil
}

// Evaluate 评估单元在 now（不含）的暴露。
func (t *Tracker) Evaluate(unitID string, now int64) (EvalResult, error) {
	if unitID == "" || now < 0 || now > maxNow {
		return EvalResult{}, probe.ErrInvalid
	}
	if err := t.checkClock(now); err != nil {
		return EvalResult{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return EvalResult{}, probe.ErrNotFound
	}
	if u.released {
		return EvalResult{E: u.frozenE, Spoiled: u.spoiledAt != 0, SpoiledAt: u.spoiledAt}, nil
	}
	t.resolveSpoil(u, now)
	return EvalResult{E: t.exposureAt(u, now), Spoiled: u.spoiledAt != 0, SpoiledAt: u.spoiledAt}, nil
}

// BMax 返回预算上限。
func (t *Tracker) BMax() int64 { return t.bmax }

// SpoiledAt 返回已确定的判废时刻。
func (t *Tracker) SpoiledAt(unitID string) (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return 0, false
	}
	return u.spoiledAt, true
}

// IsOnDevice 报告单元当前是否在某设备上。
func (t *Tracker) IsOnDevice(unitID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil || u.released || !u.onDevice {
		return "", false
	}
	return u.device, true
}

// Exists 报告单元是否已登记。
func (t *Tracker) Exists(unitID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.units[unitID] != nil
}

// IsReleased 报告单元是否已放行。
func (t *Tracker) IsReleased(unitID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	return u != nil && u.released
}

// PureE 只读计算 E(unit, now)，不改变状态。
func (t *Tracker) PureE(unitID string, now int64) (int64, error) {
	if unitID == "" || now < 0 || now > maxNow {
		return 0, probe.ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return 0, probe.ErrNotFound
	}
	return t.exposureAt(u, now), nil
}

// SpoiledBy 只读报告 now（含）前是否判废，不改变持久状态。
func (t *Tracker) SpoiledBy(unitID string, now int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return false
	}
	if u.released {
		return u.frozenE > t.bmax
	}
	shadow := *u
	t.resolveSpoil(&shadow, now)
	return shadow.spoiledAt != 0
}

// MarkReleased 以 now 冻结单元，视为同时卸下；返回冻结 E。
func (t *Tracker) MarkReleased(unitID string, now int64) (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.units[unitID]
	if u == nil {
		return 0, false
	}
	if u.released {
		return u.frozenE, true
	}
	t.closeOpen(u, now)
	u.onDevice = false
	u.device = ""
	u.released = true
	var e int64
	for _, sp := range u.spans {
		e += t.spanWeight(sp, sp.start, sp.end)
	}
	u.frozenE = e
	u.frozenAt = now
	return u.frozenE, true
}
