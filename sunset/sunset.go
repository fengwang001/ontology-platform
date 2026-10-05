// Package sunset 实现数据集从弃用到下线的分阶段闸门：
// 阶段推进判定、递增式限流演练与延期，并统一实施拒绝次序。
package sunset

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/registry"
	"ontology/usage"
)

const maxTimestamp = int64(1_000_000_000_000)

// MaxExtensions 为每个数据集允许的最多延期次数。
const MaxExtensions = 2

var (
	ErrInvalidParam    = errors.New("sunset: invalid parameter")
	ErrClockRegression = errors.New("sunset: clock regression")
	ErrDatasetNotFound = errors.New("sunset: dataset not found")
	ErrDatasetExists   = errors.New("sunset: dataset already exists")
	ErrPhase           = errors.New("sunset: phase mismatch")
	ErrNoticeTooShort  = errors.New("sunset: notice shorter than minimum")
	ErrTooEarly        = errors.New("sunset: too early to advance")
	ErrDownstream      = errors.New("sunset: downstream not retired")
	ErrConsumers       = errors.New("sunset: active consumers remain")
	ErrBrownout        = errors.New("sunset: brownout window")
	ErrRetired         = errors.New("sunset: dataset retired")
	ErrNotConsumer     = errors.New("sunset: not an active consumer")
	ErrExtendLimit     = errors.New("sunset: extension count limit")
	ErrTooLong         = errors.New("sunset: cumulative extension too long")
)

// ListError 携带阻塞者名单（ErrDownstream / ErrConsumers），errors.Is 可区分。
type ListError struct {
	Kind  error
	Names []string
}

func (e *ListError) Error() string { return e.Kind.Error() + ": " + joinNames(e.Names) }
func (e *ListError) Unwrap() error { return e.Kind }

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }

// Engine 为分阶段闸门引擎，所有操作可并发调用，等价于某串行顺序。
type Engine struct {
	mu     sync.Mutex
	nmin   int64
	bw     int64
	pd     int64
	x      int64
	q      int64
	xmax   int64
	reg    *registry.Registry
	led    *usage.Ledger
	maxNow int64
	hasNow bool
}

// NewEngine 校验构造参数并创建引擎。
func NewEngine(nmin, bw, pd, x, q, xmax int64) (*Engine, error) {
	if nmin <= 0 || bw <= 0 || pd <= 0 || x <= 0 || q <= 0 || xmax <= 0 {
		return nil, fmt.Errorf("%w: all parameters must be positive", ErrInvalidParam)
	}
	if bw > nmin {
		return nil, fmt.Errorf("%w: brownout window exceeds min notice", ErrInvalidParam)
	}
	if x > pd {
		return nil, fmt.Errorf("%w: brownout step exceeds period", ErrInvalidParam)
	}
	return &Engine{
		nmin: nmin, bw: bw, pd: pd, x: x, q: q, xmax: xmax,
		reg: registry.New(),
		led: usage.New(),
	}, nil
}

// checkClock 实施"参数非法 > 时钟回退"的公共前置校验。
func (e *Engine) checkClock(now int64) error {
	if now < 0 || now > maxTimestamp {
		return fmt.Errorf("%w: now out of range", ErrInvalidParam)
	}
	if e.hasNow && now < e.maxNow {
		return fmt.Errorf("%w: now %d < %d", ErrClockRegression, now, e.maxNow)
	}
	return nil
}

// commitClock 仅在操作被完全接受后推进时钟。
func (e *Engine) commitClock(now int64) {
	if !e.hasNow || now > e.maxNow {
		e.maxNow, e.hasNow = now, true
	}
}

func (e *Engine) dataset(name string) (*registry.Dataset, error) {
	d, ok := e.reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDatasetNotFound, name)
	}
	return d, nil
}

// AddDataset 登记数据集，parents 为 0 到 8 个已登记的上游。
func (e *Engine) AddDataset(name string, parents []string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if name == "" || len(parents) > 8 {
		return fmt.Errorf("%w: bad name or too many parents", ErrInvalidParam)
	}
	seen := make(map[string]bool, len(parents))
	for _, p := range parents {
		if p == "" || seen[p] {
			return fmt.Errorf("%w: bad or duplicate parent", ErrInvalidParam)
		}
		seen[p] = true
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if _, ok := e.reg.Get(name); ok {
		return fmt.Errorf("%w: %q", ErrDatasetExists, name)
	}
	for _, p := range parents {
		if _, ok := e.reg.Get(p); !ok {
			return fmt.Errorf("%w: parent %q", ErrDatasetNotFound, p)
		}
	}
	e.reg.Add(name, parents)
	e.commitClock(now)
	return nil
}

// Deprecate 弃用数据集，返回受影响（未 Retired 的传递下游）名单。
func (e *Engine) Deprecate(d string, notice, now int64) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d == "" {
		return nil, fmt.Errorf("%w: empty dataset", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	ds, err := e.dataset(d)
	if err != nil {
		return nil, err
	}
	if ds.Phase != registry.Active {
		return nil, fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	if notice < e.nmin {
		return nil, fmt.Errorf("%w: %d < %d", ErrNoticeTooShort, notice, e.nmin)
	}
	ds.Phase = registry.Deprecated
	ds.SunsetAt = now + notice
	ds.BrownStart = ds.SunsetAt - e.bw
	e.commitClock(now)
	return e.reg.Impact(d), nil
}

// Undeprecate 把 Deprecated 数据集撤回为 Active，延期计数不清零。
func (e *Engine) Undeprecate(d string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d == "" {
		return fmt.Errorf("%w: empty dataset", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	ds, err := e.dataset(d)
	if err != nil {
		return err
	}
	if ds.Phase != registry.Deprecated {
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	ds.Phase = registry.Active
	e.commitClock(now)
	return nil
}

// Access 判定一次访问是否放行；放行时返回是否带 Warning。
func (e *Engine) Access(consumer, d string, now int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if consumer == "" || d == "" {
		return false, fmt.Errorf("%w: empty consumer or dataset", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return false, err
	}
	ds, err := e.dataset(d)
	if err != nil {
		return false, err
	}
	switch ds.Phase {
	case registry.Active:
		e.led.RecordAccess(d, consumer, now)
		e.commitClock(now)
		return false, nil
	case registry.Deprecated:
		e.led.RecordAccess(d, consumer, now)
		e.commitClock(now)
		return true, nil
	case registry.Brownout:
		elapsed := now - ds.BrownStart
		i := elapsed / e.pd
		o := elapsed % e.pd
		if limit := (i + 1) * e.x; o < min(limit, e.pd) {
			return false, fmt.Errorf("%w: %s at %d", ErrBrownout, d, now)
		}
		e.led.RecordAccess(d, consumer, now)
		e.commitClock(now)
		return true, nil
	default: // registry.Retired
		return false, fmt.Errorf("%w: %s", ErrRetired, d)
	}
}

// Ack 确认消费者已迁移。
func (e *Engine) Ack(consumer, d string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if consumer == "" || d == "" {
		return fmt.Errorf("%w: empty consumer or dataset", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if _, err := e.dataset(d); err != nil {
		return err
	}
	if !e.led.HasAccess(d, consumer) {
		return fmt.Errorf("%w: %q never accessed %q", ErrNotConsumer, consumer, d)
	}
	e.led.Ack(d, consumer)
	e.commitClock(now)
	return nil
}

// Advance 把数据集推进到下一阶段，每次只前进一步。
func (e *Engine) Advance(d string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d == "" {
		return fmt.Errorf("%w: empty dataset", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	ds, err := e.dataset(d)
	if err != nil {
		return err
	}
	switch ds.Phase {
	case registry.Deprecated:
		if now < ds.BrownStart {
			return fmt.Errorf("%w: brownout starts at %d", ErrTooEarly, ds.BrownStart)
		}
		ds.Phase = registry.Brownout
		e.commitClock(now)
		return nil
	case registry.Brownout:
		if now < ds.SunsetAt {
			return fmt.Errorf("%w: sunset at %d", ErrTooEarly, ds.SunsetAt)
		}
		var blocked []string
		for _, c := range ds.Children {
			child, _ := e.reg.Get(c)
			if child.Phase != registry.Retired {
				blocked = append(blocked, c)
			}
		}
		if len(blocked) > 0 {
			sortStrings(blocked)
			return &ListError{Kind: ErrDownstream, Names: blocked}
		}
		if active := e.led.ActiveConsumers(d, now, e.q); len(active) > 0 {
			return &ListError{Kind: ErrConsumers, Names: active}
		}
		ds.Phase = registry.Retired
		e.commitClock(now)
		return nil
	default:
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
}

// Extend 由活跃消费者申请延期，sunsetAt 与 brownStart 同时后移 extra。
func (e *Engine) Extend(d, consumer string, extra, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d == "" || consumer == "" || extra <= 0 {
		return fmt.Errorf("%w: bad dataset, consumer or extra", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	ds, err := e.dataset(d)
	if err != nil {
		return err
	}
	if ds.Phase != registry.Deprecated {
		return fmt.Errorf("%w: %s is %s", ErrPhase, d, ds.Phase)
	}
	if !e.led.IsActive(d, consumer, now, e.q) {
		return fmt.Errorf("%w: %q not active on %q", ErrNotConsumer, consumer, d)
	}
	if ds.ExtCount >= MaxExtensions {
		return fmt.Errorf("%w: %d extensions used", ErrExtendLimit, ds.ExtCount)
	}
	if ds.ExtTotal+extra > e.xmax {
		return fmt.Errorf("%w: %d+%d > %d", ErrTooLong, ds.ExtTotal, extra, e.xmax)
	}
	ds.SunsetAt += extra
	ds.BrownStart += extra
	ds.ExtCount++
	ds.ExtTotal += extra
	e.commitClock(now)
	return nil
}
