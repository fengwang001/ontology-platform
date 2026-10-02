package ontology

import (
	"errors"
	"math/bits"
	"sync"
)

// Direction 记录最近一次改变（Applied 或强制收缩）的方向。
type Direction int

const (
	DirNone Direction = iota
	DirUp
	DirDown
)

func (d Direction) String() string {
	switch d {
	case DirUp:
		return "up"
	case DirDown:
		return "down"
	default:
		return "none"
	}
}

// Action 是 Sample 与 SetChannels 的处理结果。
type Action int

const (
	ActionRejected Action = iota // 参数非法或暂停（Sample）/容量不足（SetChannels）
	ActionSkipped                // 污染样本：仅清除 pol
	ActionHold
	ActionPending // 增大已累计一次确认，尚未达到 need
	ActionApplied // 增大或缩小生效
	ActionForced  // SetChannels 触发的强制收缩
	ActionNoop    // SetChannels 通道数未变
	ActionOK      // SetChannels 成功且未收缩；Pause/Resume
)

func (a Action) String() string {
	switch a {
	case ActionSkipped:
		return "Skipped"
	case ActionHold:
		return "Hold"
	case ActionPending:
		return "Pending"
	case ActionApplied:
		return "Applied"
	case ActionForced:
		return "Forced"
	case ActionNoop:
		return "Noop"
	case ActionOK:
		return "OK"
	default:
		return "Rejected"
	}
}

// Config 是去膨胀控制器的构造参数。
type Config struct {
	Bmin uint64 // 缓冲下限
	Bmax uint64 // 缓冲上限
	G    uint64 // 粒度
	B0   uint64 // 初始大小
	T    uint64 // 目标时延（毫秒，1..1e6）
	W    int    // 滑动窗口样本数（1..100）
	ThU  uint64 // 增大阈值百分比（0..1000）
	ThD  uint64 // 缩小阈值百分比（0..100）
	Kc   int    // 增大确认次数（1..100）
	C0   int    // 初始通道数（1..1e4）
	Pool uint64 // 内存池字节数（1..2^40）
	H    int    // 振荡抑制窗口（0..1000）
}

// SampleResult 是一次 Sample 的返回值。
type SampleResult struct {
	Action Action
	Cur    uint64 // 当前大小
	R      uint64 // 窗口吞吐率（字节/秒，向下取整）；Skipped/Rejected 时为 0
	Cand   uint64 // 候选大小；Skipped/Rejected 时为 0
	Streak int
}

// StateResult 是 State 查询返回的全部状态。
type StateResult struct {
	Cur          uint64
	Channels     int
	WindowLen    int
	WindowBytes  []uint64 // 按时间顺序（最旧在前）复制
	WindowDt     []uint64
	Streak       int
	Polluted     bool
	Paused       bool
	LastDir      Direction
	Gap          int
	Damp         int
	AppliedCount int64
	ForcedCount  int64
	SkippedCount int64
	WindowOps    int64
}

var (
	ErrInvalidConfig = errors.New("ontology: invalid deflator config")
	ErrInvalidArg    = errors.New("ontology: invalid argument")
	ErrPaused        = errors.New("ontology: controller is paused")
	ErrCapacity      = errors.New("ontology: effective buffer capacity below Bmin")
)

// Deflator 是网络缓冲去膨胀控制器。
type Deflator struct {
	mu sync.Mutex

	cfg Config

	cur uint64
	ch  int

	// 环形滑动窗口；head 指向下一个写入位置，full 表示已写满 W 个槽。
	ring [100]winSample
	wb   uint64 // 窗口内 bytes 总和
	wd   uint64 // 窗口内 dt 总和
	head int
	full bool

	streak   int
	pol      bool
	paused   bool
	lastDir  Direction
	gap      int
	damp     int
	appliedN int64
	forcedN  int64
	skippedN int64
	winOps   int64
}

type winSample struct {
	bytes uint64
	dt    uint64
}

func beff(bmax, pool uint64, c int, g uint64) uint64 {
	v := pool / (uint64(c) * g) * g
	if v > bmax {
		return bmax
	}
	return v
}

// div128_64 返回 floor(hi<<64+lo)/y，y 必须非零；商须落在 uint64 内。
func div128_64(hi, lo, y uint64) uint64 {
	q, _ := bits.Div64(hi, lo, y)
	return q
}

// ceilDiv 返回 ceil(a/b)，b 必须非零。
func ceilDiv(a, b uint64) uint64 {
	return (a + b - 1) / b
}

// NewDeflator 构造控制器；配置非法时返回 ErrInvalidConfig。
func NewDeflator(cfg Config) (*Deflator, error) {
	if cfg.G < 1 || cfg.Bmin < cfg.G || cfg.B0 < cfg.Bmin || cfg.Bmax < cfg.B0 || cfg.Bmax > 1<<30 {
		return nil, ErrInvalidConfig
	}
	if cfg.Bmin%cfg.G != 0 || cfg.Bmax%cfg.G != 0 || cfg.B0%cfg.G != 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.T < 1 || cfg.T > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	if cfg.W < 1 || cfg.W > 100 {
		return nil, ErrInvalidConfig
	}
	if cfg.ThU > 1000 || cfg.ThD > 100 {
		return nil, ErrInvalidConfig
	}
	if cfg.Kc < 1 || cfg.Kc > 100 {
		return nil, ErrInvalidConfig
	}
	if cfg.C0 < 1 || cfg.C0 > 10_000 {
		return nil, ErrInvalidConfig
	}
	if cfg.Pool < 1 || cfg.Pool > 1<<40 {
		return nil, ErrInvalidConfig
	}
	if cfg.H < 0 || cfg.H > 1000 {
		return nil, ErrInvalidConfig
	}
	if beff(cfg.Bmax, cfg.Pool, cfg.C0, cfg.G) < cfg.B0 {
		return nil, ErrInvalidConfig
	}

	d := &Deflator{
		cfg: cfg,
		cur: cfg.B0,
		ch:  cfg.C0,
	}
	return d, nil
}

// Sample 录入一个采样周期。
func (d *Deflator) Sample(bytes, dt uint64) (SampleResult, error) {
	if bytes > 1<<30 || dt < 1 || dt > 1_000_000 {
		return SampleResult{}, ErrInvalidArg
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.paused {
		return SampleResult{}, ErrPaused
	}
	if d.pol {
		d.pol = false
		d.skippedN++
		return SampleResult{Action: ActionSkipped, Cur: d.cur, Streak: d.streak}, nil
	}

	if d.lastDir != DirNone {
		d.gap++
	}

	// 入窗：满则先挤出最旧样本（1 出 + 1 入 = 2 次窗口操作）。
	if d.full {
		old := d.ring[d.head]
		d.wb -= old.bytes
		d.wd -= old.dt
		d.winOps++
	}
	d.ring[d.head] = winSample{bytes: bytes, dt: dt}
	d.wb += bytes
	d.wd += dt
	if d.head == d.cfg.W-1 {
		d.head = 0
		d.full = true
	} else {
		d.head++
	}
	d.winOps++

	// R = floor(Sb*1000/Sd)，Sb*1000 用 128 位计算。
	hi, lo := bits.Mul64(d.wb, 1000)
	r := div128_64(hi, lo, d.wd)
	// Dt = ceil(R*T/1000)，R*T 同样走 128 位（可达 128 位量级）。
	hi, lo = bits.Mul64(r, d.cfg.T)
	// ceil((hi:lo)/1000)
	var dtTarget uint64
	if hi == 0 {
		dtTarget = ceilDiv(lo, 1000)
	} else {
		q, rem := bits.Div64(hi, lo, 1000)
		if rem != 0 {
			q++ // q 远小于 2^64
		}
		dtTarget = q
	}
	per := ceilDiv(dtTarget, uint64(d.ch))

	eff := beff(d.cfg.Bmax, d.cfg.Pool, d.ch, d.cfg.G)
	raw := per
	if raw < d.cfg.Bmin {
		raw = d.cfg.Bmin
	}
	if raw > eff {
		raw = eff
	}
	cand := raw / d.cfg.G * d.cfg.G

	res := SampleResult{Cur: d.cur, R: r, Cand: cand}

	startDamp := d.damp
	dampSetThisSample := false

	switch {
	case cand == d.cur:
		d.streak = 0
		res.Action = ActionHold
	case cand > d.cur:
		delta := cand - d.cur
		if delta*100 >= d.cur*d.cfg.ThU || cand == eff {
			d.streak++
			need := d.cfg.Kc
			if startDamp > 0 {
				need = 2 * d.cfg.Kc
			}
			if d.streak >= need {
				d.cur = cand
				d.streak = 0
				d.pol = true
				d.appliedN++
				dampSetThisSample = d.registerChange(DirUp)
				res.Cur = d.cur
				res.Streak = 0
				res.Action = ActionApplied
			} else {
				res.Streak = d.streak
				res.Action = ActionPending
			}
		} else {
			d.streak = 0
			res.Streak = 0
			res.Action = ActionHold
		}
	default: // cand < d.cur
		delta := d.cur - cand
		if delta*100 >= d.cur*d.cfg.ThD || cand == d.cfg.Bmin {
			d.cur = cand
			d.streak = 0
			d.pol = true
			d.appliedN++
			dampSetThisSample = d.registerChange(DirDown)
			res.Cur = d.cur
			res.Streak = 0
			res.Action = ActionApplied
		} else {
			d.streak = 0
			res.Streak = 0
			res.Action = ActionHold
		}
	}

	if d.damp > 0 && !dampSetThisSample {
		d.damp--
	}

	return res, nil
}

// SetChannels 修改通道数，必要时强制收缩。
func (d *Deflator) SetChannels(c int) (Action, uint64, error) {
	if c < 1 || c > 10_000 {
		return ActionRejected, 0, ErrInvalidArg
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if c == d.ch {
		return ActionNoop, d.cur, nil
	}

	eff := beff(d.cfg.Bmax, d.cfg.Pool, c, d.cfg.G)
	if eff < d.cfg.Bmin {
		return ActionRejected, d.cur, ErrCapacity
	}

	d.ch = c
	d.streak = 0
	if d.cur > eff {
		d.cur = eff
		d.pol = true
		d.forcedN++
		d.registerChange(DirDown)
		return ActionForced, d.cur, nil
	}
	return ActionOK, d.cur, nil
}

// Pause 置暂停标记。
func (d *Deflator) Pause() {
	d.mu.Lock()
	d.paused = true
	d.mu.Unlock()
}

// Resume 清除暂停标记并清空窗口、streak；其余状态保留。
func (d *Deflator) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = false
	d.head = 0
	d.full = false
	d.wb = 0
	d.wd = 0
	d.streak = 0
}

// State 返回全部状态的快照。
func (d *Deflator) State() StateResult {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := d.cfg.W
	if !d.full {
		n = d.head
	}
	ws := make([]uint64, n)
	wt := make([]uint64, n)
	start := d.head
	if d.full {
		// 最旧样本在 head 处（下一个将被挤出的位置）。
	} else {
		start = 0
	}
	for i := 0; i < n; i++ {
		idx := (start + i) % d.cfg.W
		ws[i] = d.ring[idx].bytes
		wt[i] = d.ring[idx].dt
	}

	return StateResult{
		Cur:          d.cur,
		Channels:     d.ch,
		WindowLen:    n,
		WindowBytes:  ws,
		WindowDt:     wt,
		Streak:       d.streak,
		Polluted:     d.pol,
		Paused:       d.paused,
		LastDir:      d.lastDir,
		Gap:          d.gap,
		Damp:         d.damp,
		AppliedCount: d.appliedN,
		ForcedCount:  d.forcedN,
		SkippedCount: d.skippedN,
		WindowOps:    d.winOps,
	}
}

// registerChange 按振荡抑制规则处理一次改变（Applied 或强制收缩）。
// 必须在 gap 已反映「改变发生时」取值的时刻调用（Sample 在 gap++ 之后、
// SetChannels 不改 gap）。返回本次调用是否刚刚设置了 damp。
func (d *Deflator) registerChange(dir Direction) bool {
	setDamp := false
	if d.lastDir != DirNone && dir != d.lastDir && d.gap <= d.cfg.H {
		if d.cfg.H > 0 {
			d.damp = d.cfg.H
			setDamp = true
		}
	}
	d.lastDir = dir
	d.gap = 0
	return setDamp
}
