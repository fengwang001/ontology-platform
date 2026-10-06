package demand

import (
	"log"
	"sync"
)

// ActionKind 为评估动作类别。
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionShed
	ActionRestore
)

func (k ActionKind) String() string {
	switch k {
	case ActionShed:
		return "切除"
	case ActionRestore:
		return "恢复"
	default:
		return "无"
	}
}

// Action 为本次评估对单个负荷执行的动作。
type Action struct {
	Kind   ActionKind
	LoadID int
	At     int64
}

// EvalResult 为一次被接受上报后的评估结果。
type EvalResult struct {
	At          int64
	Actions     []Action
	StillExceed bool
	Forecasts   []forecast
}

// PeakRecord 为历史最高实测需量记录；并列取较早窗口结束时刻。
type PeakRecord struct {
	PowerKW *frac
	EndAt   int64
}

// Controller 为并发安全的最大需量控制器。
// 单个互斥锁把所有操作串行化，因此并发结果等价于某个串行顺序。
type Controller struct {
	mu sync.Mutex

	cfg    Config
	book   *loadBook
	ring   *windowRing
	pred   *predictor
	logger *log.Logger

	lastReport int64 // 上次被接受上报时刻；-1 表示尚无
	lastP      *frac // 最近一次被接受上报区间功率（千瓦）
	now        int64 // 当前逻辑时刻（上报/运维推进）

	peak *PeakRecord
}

// Option 为控制器可选配置。
type Option func(*Controller)

// WithLogger 设置判定日志（打印输入、输出与判定依据）。
func WithLogger(l *log.Logger) Option {
	return func(c *Controller) { c.logger = l }
}

// New 创建控制器。
func New(cfg Config, opts ...Option) (*Controller, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ring := newWindowRing(cfg)
	c := &Controller{
		cfg:        cfg,
		book:       newLoadBook(),
		ring:       ring,
		pred:       newPredictor(cfg, ring),
		lastReport: -1,
		lastP:      newFrac(),
		now:        -1,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Controller) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}

// Report 接受用电上报并在成功后评估一次。
// energyKWS 为自上次被接受上报以来的用电量（千瓦秒），不得为负。
func (c *Controller) Report(t int64, energyKWS int64) (*EvalResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 错误次序：参数非法 > 数据非法 > 时刻回退。
	if t < 0 {
		return nil, errParam("上报时刻不得为负，实际为 %d", t)
	}
	if energyKWS < 0 {
		return nil, errData("用电量不得为负，实际为 %d", energyKWS)
	}
	prev := c.lastReport
	if prev >= 0 && t <= prev {
		return nil, errRewind("上报时刻 %d 不严格大于上次被接受时刻 %d", t, prev)
	}
	// 区间平均功率物理上限校验（区间内功率视为恒定）。
	if prev >= 0 {
		limitE := c.cfg.MaxPowerKW * (t - prev)
		if energyKWS > limitE {
			return nil, errData(
				"区间 (%d,%d] 平均功率 %.6fkW 超过物理上限 %dkW",
				prev, t, float64(energyKWS)/float64(t-prev), c.cfg.MaxPowerKW)
		}
	} else if t > 0 && energyKWS > c.cfg.MaxPowerKW*t {
		return nil, errData(
			"区间 (0,%d] 平均功率 %.6fkW 超过物理上限 %dkW",
			t, float64(energyKWS)/float64(t), c.cfg.MaxPowerKW)
	}

	// 被拒绝的上报不改变任何状态；以下才是提交阶段。
	energy := fracInt(energyKWS)
	avgP := newFrac()
	if prev < 0 {
		if t > 0 {
			avgP = fracOver(energyKWS, t)
			c.pred.pNum, c.pred.pDen = energyKWS, t
		} else {
			c.pred.pNum, c.pred.pDen = 0, 1
		}
		c.ring.advance(0, t, energy, c.onWindowClosed)
	} else {
		avgP = fracOver(energyKWS, t-prev)
		c.pred.pNum, c.pred.pDen = energyKWS, t-prev
		c.ring.advance(prev, t, energy, c.onWindowClosed)
	}
	c.lastReport = t
	c.lastP = avgP
	c.pred.lastP = avgP
	if t > c.now {
		c.now = t
	}

	res := c.evaluate(t)
	c.logf("[上报] t=%d 用电量=%dkW·s 区间功率=%.6fkW => 动作=%v 仍越限=%v",
		t, energyKWS, avgP.float64(), actionSummary(res.Actions), res.StillExceed)
	return res, nil
}

// onWindowClosed 为窗口结束回调：记录实测需量并更新峰值。
// 窗口用电量由环在覆盖对应槽前计算并随回调传入。
func (c *Controller) onWindowClosed(end int64, e *frac) {
	// 窗口始终为完整长度；早于首次上报的部分按零用电计入平均。
	power := e.clone().mulRat(fracOver(1, c.cfg.WindowSec))
	c.logf("[关窗] end=%d 实测平均=%.6fkW", end, power.float64())
	if c.peak == nil || power.cmp(c.peak.PowerKW) > 0 {
		c.peak = &PeakRecord{PowerKW: power.clone(), EndAt: end}
	}
	// 并列（相等）保留较早者：不更新。
}

// evaluate 在 now 时刻执行一次评估（仅被接受上报后调用）。
func (c *Controller) evaluate(now int64) *EvalResult {
	res := &EvalResult{At: now}

	c.pred.prepare(now)
	fs, exceeds := c.pred.evaluate(now, 0)
	res.Forecasts = fs
	for _, f := range fs {
		c.logf("[预测] end=%d 预测未来=%.6f 剩余余量=%.6f 越限=%v",
			f.end, f.future.float64(), f.limit.float64(), f.exceeds)
	}

	if exceeds {
		cands := c.book.shedCandidates(now)
		req := c.pred.requiredShed()
		plan := chooseShed(cands, req, c.pred.feasible)
		res.StillExceed = plan.stillExceed
		for _, id := range plan.ids {
			l, _ := c.book.get(id)
			l.on = false
			l.since = now
			res.Actions = append(res.Actions, Action{Kind: ActionShed, LoadID: id, At: now})
		}
		c.logf("[切除] 需切除>=%.6fkW 切除=%v 仍越限=%v",
			req.float64(), plan.ids, plan.stillExceed)
		return res
	}

	// 未越限才恢复；同一次评估不会既切除又恢复。
	cands := c.book.restoreCandidates(now)
	ids := chooseRestore(cands, func(addedKW int64) bool { return c.pred.feasible(-addedKW) })
	for _, id := range ids {
		l, _ := c.book.get(id)
		l.on = true
		l.since = now
		res.Actions = append(res.Actions, Action{Kind: ActionRestore, LoadID: id, At: now})
	}
	if len(ids) > 0 {
		c.logf("[恢复] 恢复=%v", ids)
	}
	return res
}

// AddLoad 增加负荷（初始接入，自 at 起计最短接入）。不触发评估。
func (c *Controller) AddLoad(at int64, spec LoadSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := spec.validate(); err != nil {
		return err
	}
	if at < 0 {
		return errParam("操作时刻不得为负，实际为 %d", at)
	}
	if _, ok := c.book.get(spec.ID); ok {
		return errParam("负荷编号 %d 已存在", spec.ID)
	}
	if err := c.checkOpTime(at); err != nil {
		return err
	}
	if err := c.book.add(at, spec); err != nil {
		return err
	}
	c.logf("[运维] 增加负荷 id=%d 额定=%dkW 优先级=%d @%d", spec.ID, spec.RatedKW, spec.Priority, at)
	return nil
}

// LockLoad 将负荷锁定为保持接入。锁定期间不参与切除；锁定已断开负荷不使其恢复。
func (c *Controller) LockLoad(at int64, id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at < 0 {
		return errParam("操作时刻不得为负，实际为 %d", at)
	}
	l, ok := c.book.get(id)
	if !ok {
		return errNotFound("负荷 %d 不存在", id)
	}
	if err := c.checkOpTime(at); err != nil {
		return err
	}
	if l.locked {
		return errState("负荷 %d 已处于锁定状态", id)
	}
	l.locked = true
	c.logf("[运维] 锁定负荷 id=%d @%d（当前接入=%v）", id, at, l.on)
	return nil
}

// UnlockLoad 解除锁定；解锁后立即恢复参与后续评估。
func (c *Controller) UnlockLoad(at int64, id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at < 0 {
		return errParam("操作时刻不得为负，实际为 %d", at)
	}
	l, ok := c.book.get(id)
	if !ok {
		return errNotFound("负荷 %d 不存在", id)
	}
	if err := c.checkOpTime(at); err != nil {
		return err
	}
	if !l.locked {
		return errState("负荷 %d 未处于锁定状态", id)
	}
	l.locked = false
	c.logf("[运维] 解锁负荷 id=%d @%d", id, at)
	return nil
}

// RemoveLoad 删除负荷。接入或断开态均拒绝，故本实现始终返回状态不允许。
func (c *Controller) RemoveLoad(at int64, id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at < 0 {
		return errParam("操作时刻不得为负，实际为 %d", at)
	}
	if _, ok := c.book.get(id); !ok {
		return errNotFound("负荷 %d 不存在", id)
	}
	return errState("负荷 %d 处于接入/断开态，不允许删除", id)
}

// checkOpTime 校验运维操作时刻不得早于当前逻辑时刻（时刻回退归为状态不允许）。
func (c *Controller) checkOpTime(at int64) error {
	if at < c.now {
		return errState("操作时刻 %d 早于当前时刻 %d", at, c.now)
	}
	if at > c.now {
		c.now = at
	}
	return nil
}

// Peak 查询历史最高实测需量；尚无窗口结束时返回 nil。
func (c *Controller) Peak() *PeakRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peak == nil {
		return nil
	}
	return &PeakRecord{PowerKW: c.peak.PowerKW.clone(), EndAt: c.peak.EndAt}
}

func actionSummary(as []Action) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Kind.String()+"#"+itoa(a.LoadID))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
