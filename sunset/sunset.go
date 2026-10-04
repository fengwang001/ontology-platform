// Package sunset 实现数据集从弃用到下线的分阶段闸门。
//
// 阶段顺序 Active -> Deprecated -> Brownout -> Retired，只由显式 Advance 推进。
// 所有公开方法接收单调不减的 now；被拒绝的操作不改变任何状态（含时钟）。
package sunset

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/registry"
	"ontology/usage"
)

// Config 是闸门的全部构造参数，均为正整数秒。
type Config struct {
	Nmin int64
	Bw   int64
	Pd   int64
	X    int64
	Q    int64
	Xmax int64
}

// 哨兵错误，errors.Is 可区分；带明细的错误用 %w 包裹对应哨兵。
var (
	ErrInvalidConfig  = errors.New("sunset: invalid config")
	ErrInvalidArg     = errors.New("sunset: invalid argument")
	ErrClockBack      = errors.New("sunset: clock moved backwards")
	ErrNotFound       = errors.New("sunset: dataset not found")
	ErrPhase          = errors.New("sunset: wrong phase")
	ErrNoticeTooShort = errors.New("sunset: notice shorter than Nmin")
	ErrTooEarly       = errors.New("sunset: too early to advance")
	ErrDownstream     = errors.New("sunset: dataset has unretired downstream")
	ErrConsumers      = errors.New("sunset: dataset still has active consumers")
	ErrNotConsumer    = errors.New("sunset: consumer has no successful access")
	ErrExtendLimit    = errors.New("sunset: extension count limit reached")
	ErrTooLong        = errors.New("sunset: cumulative extension exceeds Xmax")
	ErrBrownout       = errors.New("sunset: access rejected during brownout window")
	ErrRetired        = errors.New("sunset: dataset is retired")
)

// detailError 在哨兵错误上附带阻塞条目（未 Retired 的直接下游 / 全部活跃消费者）。
type detailError struct {
	wrap  error
	items []string
}

func (e *detailError) Error() string {
	return e.wrap.Error() + ": " + strings.Join(e.items, ",")
}

func (e *detailError) Unwrap() error { return e.wrap }

// AccessResult 是一次 Access 的判定结果。
type AccessResult struct {
	Allowed bool
	Warning bool
	Reason  string
}

// ErrorItems 从带明细列表的错误中取出条目（如阻塞的下游/消费者）；无明细时返回 nil。
func ErrorItems(err error) []string {
	var de *detailError
	if errors.As(err, &de) {
		return append([]string(nil), de.items...)
	}
	return nil
}

// Gate 是分阶段闸门。所有方法可并发调用，等价于某个串行顺序。
type Gate struct {
	cfg     Config
	mu      sync.Mutex
	reg     *registry.Registry
	track   *usage.Tracker
	lastNow int64
}

// New 创建闸门；参数必须为正整数且 Bw<=Nmin、X<=Pd，否则返回 ErrInvalidConfig。
func New(cfg Config) (*Gate, error) {
	if cfg.Nmin <= 0 || cfg.Bw <= 0 || cfg.Pd <= 0 || cfg.X <= 0 || cfg.Q <= 0 || cfg.Xmax <= 0 ||
		cfg.Bw > cfg.Nmin || cfg.X > cfg.Pd {
		return nil, ErrInvalidConfig
	}
	return &Gate{cfg: cfg, reg: registry.New(), track: usage.New()}, nil
}

func validName(s string) bool { return s != "" }

// clockCheck 在“数据集存在/阶段”等业务校验之前调用；参数非法优先于时钟回退，由调用方先校验参数。
func (g *Gate) clockCheck(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return fmt.Errorf("%w: now out of range", ErrInvalidArg)
	}
	if now < g.lastNow {
		return ErrClockBack
	}
	return nil
}

// AddDataset 登记数据集。parents 必须全部已登记（至多 8 个），新数据集为 Active。
func (g *Gate) AddDataset(name string, parents []string, now int64) error {
	if !validName(name) {
		return fmt.Errorf("%w: empty dataset name", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return err
	}
	switch err := g.reg.Add(name, parents); {
	case err == nil:
		g.lastNow = now
		return nil
	case errors.Is(err, registry.ErrExists):
		return fmt.Errorf("%w: %s", err, name)
	default:
		return fmt.Errorf("%w: %v", ErrInvalidArg, err)
	}
}

// Deprecate 仅 Active 可弃用；返回 d 的全部传递下游中尚未 Retired 者（字节序升序）。
func (g *Gate) Deprecate(d string, notice, now int64) ([]string, error) {
	if !validName(d) || notice < 0 {
		return nil, fmt.Errorf("%w: bad deprecate arguments", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return nil, err
	}
	ds, ok := g.reg.Get(d)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	if ds.Phase != registry.Active {
		return nil, fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	if notice < g.cfg.Nmin {
		return nil, fmt.Errorf("%w: notice %d < Nmin %d", ErrNoticeTooShort, notice, g.cfg.Nmin)
	}
	affected := g.reg.AffectedDownstream(d)
	ds.Phase = registry.Deprecated
	ds.SunsetAt = now + notice
	ds.BrownStart = ds.SunsetAt - g.cfg.Bw
	g.lastNow = now
	return affected, nil
}

// Undeprecate 仅 Deprecated 阶段可撤回为 Active；延期次数与累计量不清零。
func (g *Gate) Undeprecate(d string, now int64) error {
	if !validName(d) {
		return fmt.Errorf("%w: empty dataset name", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return err
	}
	ds, ok := g.reg.Get(d)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	if ds.Phase != registry.Deprecated {
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	ds.Phase = registry.Active
	g.lastNow = now
	return nil
}

// Access 判定某消费者在 now 对 d 的访问是否放行；仅放行的访问记录 lastAccess。
func (g *Gate) Access(consumer, d string, now int64) (AccessResult, error) {
	if !validName(consumer) || !validName(d) {
		return AccessResult{}, fmt.Errorf("%w: empty name", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return AccessResult{}, err
	}
	ds, ok := g.reg.Get(d)
	if !ok {
		return AccessResult{}, fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	switch ds.Phase {
	case registry.Active:
		g.track.Record(consumer, d, now)
		g.lastNow = now
		return AccessResult{Allowed: true, Reason: "Active: allow"}, nil
	case registry.Deprecated:
		g.track.Record(consumer, d, now)
		g.lastNow = now
		return AccessResult{
			Allowed: true,
			Warning: true,
			Reason:  fmt.Sprintf("Deprecated at %d (brownStart=%d): allow with warning", now, ds.BrownStart),
		}, nil
	case registry.Retired:
		return AccessResult{}, fmt.Errorf("%w: %s", ErrRetired, d)
	default: // Brownout：窗口内拒绝，窗口外放行并带 Warning。
		delta := now - ds.BrownStart
		i := delta / g.cfg.Pd
		o := delta % g.cfg.Pd
		shut := (i + 1) * g.cfg.X
		if shut > g.cfg.Pd {
			shut = g.cfg.Pd
		}
		if o < shut {
			return AccessResult{}, fmt.Errorf("%w: %s cycle %d offset %d < reject %d",
				ErrBrownout, d, i, o, shut)
		}
		g.track.Record(consumer, d, now)
		g.lastNow = now
		return AccessResult{
			Allowed: true,
			Warning: true,
			Reason:  fmt.Sprintf("Brownout cycle %d offset %d >= reject %d: allow with warning", i, o, shut),
		}, nil
	}
}

// Ack 表示消费者确认已迁移，要求它对 d 有过被放行的访问。
func (g *Gate) Ack(consumer, d string, now int64) error {
	if !validName(consumer) || !validName(d) {
		return fmt.Errorf("%w: empty name", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return err
	}
	if _, ok := g.reg.Get(d); !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	if !g.track.HasAccessed(consumer, d) {
		return fmt.Errorf("%w: %s has no successful access to %s", ErrNotConsumer, consumer, d)
	}
	g.track.Ack(consumer, d, now)
	g.lastNow = now
	return nil
}

// Advance 每次只把 d 推进一步；失败时按 ErrTooEarly > ErrDownstream > ErrConsumers 报错。
func (g *Gate) Advance(d string, now int64) error {
	if !validName(d) {
		return fmt.Errorf("%w: empty dataset name", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return err
	}
	ds, ok := g.reg.Get(d)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	switch ds.Phase {
	case registry.Active, registry.Retired:
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	case registry.Deprecated:
		if now < ds.BrownStart {
			return fmt.Errorf("%w: %d < brownStart %d", ErrTooEarly, now, ds.BrownStart)
		}
		ds.Phase = registry.Brownout
		g.lastNow = now
		return nil
	default: // Brownout -> Retired
		if now < ds.SunsetAt {
			return fmt.Errorf("%w: %d < sunsetAt %d", ErrTooEarly, now, ds.SunsetAt)
		}
		if blocking := g.reg.UnretiredChildren(d); len(blocking) > 0 {
			return &detailError{wrap: ErrDownstream, items: blocking}
		}
		active := g.track.ActiveConsumers(d, now, g.cfg.Q)
		if len(active) > 0 {
			return &detailError{wrap: ErrConsumers, items: active}
		}
		ds.Phase = registry.Retired
		g.lastNow = now
		return nil
	}
}

// Extend 仅 Deprecated 阶段可延期，申请者须为 d 的活跃消费者；sunsetAt 与 brownStart 同步后移。
func (g *Gate) Extend(d, consumer string, extra, now int64) error {
	if !validName(d) || !validName(consumer) || extra <= 0 {
		return fmt.Errorf("%w: bad extend arguments", ErrInvalidArg)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.clockCheck(now); err != nil {
		return err
	}
	ds, ok := g.reg.Get(d)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, d)
	}
	if ds.Phase != registry.Deprecated {
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	active := g.track.ActiveConsumers(d, now, g.cfg.Q)
	if idx := sort.SearchStrings(active, consumer); idx == len(active) || active[idx] != consumer {
		return fmt.Errorf("%w: %s is not an active consumer of %s", ErrNotConsumer, consumer, d)
	}
	if ds.ExtendCnt >= 2 {
		return fmt.Errorf("%w: %s already extended %d times", ErrExtendLimit, d, ds.ExtendCnt)
	}
	if ds.ExtendSum+extra > g.cfg.Xmax {
		return fmt.Errorf("%w: %d + %d > Xmax %d", ErrTooLong, ds.ExtendSum, extra, g.cfg.Xmax)
	}
	ds.ExtendCnt++
	ds.ExtendSum += extra
	ds.SunsetAt += extra
	ds.BrownStart += extra
	g.lastNow = now
	return nil
}

// Phase 返回数据集当前阶段；不存在返回 false。
func (g *Gate) Phase(d string) (registry.Phase, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ds, ok := g.reg.Get(d)
	if !ok {
		return registry.Active, false
	}
	return ds.Phase, true
}

// Times 返回计划下线时刻与演练起点；不存在返回 false。
func (g *Gate) Times(d string) (sunsetAt, brownStart int64, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ds, exists := g.reg.Get(d)
	if !exists {
		return 0, 0, false
	}
	return ds.SunsetAt, ds.BrownStart, true
}

// ExtendInfo 返回已用延期次数与累计延期秒数；不存在返回 false。
func (g *Gate) ExtendInfo(d string) (count int, sum int64, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ds, exists := g.reg.Get(d)
	if !exists {
		return 0, 0, false
	}
	return ds.ExtendCnt, ds.ExtendSum, true
}

// Scanned 返回 d 最近一次 Advance 消费者判定考察的消费者数（非导出计数器的观察口）。
func (g *Gate) Scanned(d string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.track.Scanned(d)
}
