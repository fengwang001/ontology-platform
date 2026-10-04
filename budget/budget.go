// Package budget 累计单元的加权超温分钟，判定判废时刻并支撑放行判定。
package budget

import (
	"ontology/probe"
	"ontology/release"
)

// Config 是预算与交接空档参数（分钟）。
type Config struct {
	H    int64 // 交接空档宽限，1..1e6
	Bmax int64 // 暴露预算，1..1e6
	Q    int64 // 复核比例（百分数），1..100
}

// Validate 校验构造参数。
func (c Config) Validate() error {
	if c.H < 1 || c.H > 1_000_000 || c.Bmax < 1 || c.Bmax > 1_000_000 ||
		c.Q < 1 || c.Q > 100 {
		return probe.ErrInvalidParam
	}
	return nil
}

// Evaluator 评估单元暴露。 Evaluate 自带时钟锁；EvaluateLocked 与 NeedsQA
// 实现 release.Evaluator，须在时钟锁内调用。
type Evaluator struct {
	clock *probe.Clock
	probe *probe.Store
	rel   *release.Store
	cfg   Config
	w     int64 // 重度权重，取自 probe 配置
}

// NewEvaluator 构造评估器并注入 release 存储。
func NewEvaluator(clock *probe.Clock, probes *probe.Store, rel *release.Store, cfg Config) (*Evaluator, error) {
	if clock == nil || probes == nil || rel == nil {
		return nil, probe.ErrInvalidParam
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ev := &Evaluator{clock: clock, probe: probes, rel: rel, cfg: cfg, w: probes.Config().W}
	rel.SetEvaluator(ev)
	return ev, nil
}

// Result 是 Evaluate 的返回值。
type Result struct {
	E            int64 // 暴露：自登记时刻到评估时刻（不含）的权重之和
	Spoiled      bool  // 是否判废：E > Bmax，恰等不判废
	SpoiledAt    int64 // 使 E > Bmax 的最小时刻
	HasSpoiledAt bool  // 未判废时 SpoiledAt 为空
}

// gapWeight 返回长度为 l 的交接空档的权重：前 min(l, H) 分钟轻度，其余重度。
func (ev *Evaluator) gapWeight(l int64) int64 {
	light := l
	if light > ev.cfg.H {
		light = ev.cfg.H
	}
	return light + (l-light)*ev.w
}

// gapCross 返回空档 [start, start+length) 内使累计 c 越过 Bmax 的时刻。
func (ev *Evaluator) gapCross(start, length, c int64) (int64, bool) {
	light := length
	if light > ev.cfg.H {
		light = ev.cfg.H
	}
	if c+light > ev.cfg.Bmax {
		return start + (ev.cfg.Bmax - c) + 1, true
	}
	if c+light+(length-light)*ev.w > ev.cfg.Bmax {
		return start + light + (ev.cfg.Bmax-c-light)/ev.w + 1, true
	}
	return 0, false
}

// evalLocked 一次顺序扫描同时算出 E(unit, x) 与 SpoiledAt。调用方须持有锁。
func (ev *Evaluator) evalLocked(u release.UnitView, x int64) (e, spoiledAt int64, spoiled bool) {
	c := int64(0)
	prev := u.RegisteredAt
	found := false
	var cross int64
	for _, iv := range u.Intervals {
		if l := iv.Start - prev; l > 0 {
			if !found {
				if cx, ok := ev.gapCross(prev, l, c); ok {
					cross, found = cx, true
				}
			}
			c += ev.gapWeight(l)
		}
		end := iv.End
		if end < 0 {
			end = x
		}
		wa := ev.probe.Weight(iv.Device, iv.Start)
		we := ev.probe.Weight(iv.Device, end)
		if !found && c+we-wa > ev.cfg.Bmax {
			if cx, ok := ev.probe.CrossAfter(iv.Device, end, ev.cfg.Bmax-c+wa); ok {
				cross, found = cx, true
			}
		}
		c += we - wa
		prev = end
	}
	if prev < x { // 尚未结束的空档按到评估时刻的长度计算
		l := x - prev
		if !found {
			if cx, ok := ev.gapCross(prev, l, c); ok {
				cross, found = cx, true
			}
		}
		c += ev.gapWeight(l)
	}
	return c, cross, found
}

// Evaluate 返回 E(unit, now)、是否判废与 SpoiledAt；已放行单元返回冻结值。
func (ev *Evaluator) Evaluate(unit string, now int64) (Result, error) {
	if unit == "" || !probe.ValidNow(now) {
		return Result{}, probe.ErrInvalidParam
	}
	ev.clock.Lock()
	defer ev.clock.Unlock()
	if err := ev.clock.Check(now); err != nil {
		return Result{}, err
	}
	u, ok := ev.rel.Unit(unit)
	if !ok {
		return Result{}, probe.ErrNotFound
	}
	if u.Released {
		ev.clock.Advance(now)
		return Result{E: u.FrozenE}, nil
	}
	e, at, spoiled := ev.evalLocked(u, now)
	res := Result{E: e, Spoiled: spoiled, SpoiledAt: at, HasSpoiledAt: spoiled}
	ev.clock.Advance(now)
	return res, nil
}

// EvaluateLocked 实现 release.Evaluator；调用方须持有时钟锁。
func (ev *Evaluator) EvaluateLocked(unit string, now int64) (int64, bool, error) {
	u, ok := ev.rel.Unit(unit)
	if !ok {
		return 0, false, probe.ErrNotFound
	}
	e, _, spoiled := ev.evalLocked(u, now)
	return e, spoiled, nil
}

// NeedsQA 报告暴露 e 是否达到复核比例：e*100 >= Bmax*Q（恰等也需要）。
func (ev *Evaluator) NeedsQA(e int64) bool {
	return e*100 >= ev.cfg.Bmax*ev.cfg.Q
}
