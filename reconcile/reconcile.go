// Package reconcile 实现曝光/点击对账器：把一批批曝光与点击事件按事件时间
// 处理成已计数曝光与归因点击，保证每个事件的归属结论与各类计数可精确复现。
//
// 规则概要：
//   - 每对 (user, ad) 独立维护事件水位、锚点（最近一次已计数曝光的时刻及
//     其是否已被归因）与锁定截止时刻 e；每个 user 维护其全部已归因点击
//     时刻的最大值 u（跨该 user 的所有 ad）。
//   - Submit 先把批内事件按 (t 升序，同刻曝光先于点击，再按输入次序)
//     稳定排序，然后依次处理，整批原子生效。
//   - 曝光：v < V 记为不可见；否则若已有锚点且 t-锚点 < C，或 t < e，
//     记为冷却；否则记为已计数并把锚点移到本曝光。
//   - 点击只看该对锚点：无锚点为无曝光；g = t-锚点，依次判定
//     g > A 已过期、g < Tmin 过快、锚点已被归因重复、
//     u 存在且 t-u < Tu 串扰，否则归因并令 e = t+Lk、u = max(u, t)。
package reconcile

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// maxParam 构造参数与可见时长 v 的上界（10^9 毫秒）。
	maxParam = int64(1_000_000_000)
	// maxTime 事件时刻 t 的上界（10^15 毫秒）。
	maxTime = int64(1_000_000_000_000_000)
)

// Kind 事件类型。
type Kind int

const (
	// Impression 曝光事件。零值保证同刻排序时曝光先于点击。
	Impression Kind = iota
	// Click 点击事件。
	Click
)

// Event 一条曝光或点击事件。V 仅对曝光有效（可见时长，毫秒）。
type Event struct {
	Kind Kind
	User string
	Ad   string
	T    int64
	V    int64
}

// Outcome 单个事件的结论，每个事件有且只有一个。
type Outcome int

const (
	Counted      Outcome = iota // 已计数（曝光）
	Cooldown                    // 冷却（曝光）
	Invisible                   // 不可见（曝光）
	Attributed                  // 归因（点击）
	Duplicate                   // 重复（点击）
	Expired                     // 已过期（点击）
	TooFast                     // 过快（点击）
	NoImpression                // 无曝光（点击）
	CrossTalk                   // 串扰（点击）
)

// NumOutcomes 结论类别总数。
const NumOutcomes = 9

var outcomeNames = [NumOutcomes]string{
	"已计数", "冷却", "不可见",
	"归因", "重复", "已过期", "过快", "无曝光", "串扰",
}

func (o Outcome) String() string {
	if o < 0 || int(o) >= NumOutcomes {
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
	return outcomeNames[o]
}

// 批级拒绝原因，可用 errors.Is 区分。
var (
	// ErrInvalidParam 参数非法：构造参数越界、user 或 ad 为空、t 或 v 越界。
	ErrInvalidParam = errors.New("reconcile: 参数非法")
	// ErrOutOfOrder 乱序：排序后任一事件的 t 小于其所属对的事件水位。
	ErrOutOfOrder = errors.New("reconcile: 事件乱序")
)

// Stats 九类结论的计数。
type Stats struct {
	Counted      int64
	Cooldown     int64
	Invisible    int64
	Attributed   int64
	Duplicate    int64
	Expired      int64
	TooFast      int64
	NoImpression int64
	CrossTalk    int64
}

// Impressions 曝光总数，恒等于 已计数+冷却+不可见。
func (s Stats) Impressions() int64 { return s.Counted + s.Cooldown + s.Invisible }

// Clicks 点击总数，恒等于 归因+重复+已过期+过快+无曝光+串扰。
func (s Stats) Clicks() int64 {
	return s.Attributed + s.Duplicate + s.Expired + s.TooFast + s.NoImpression + s.CrossTalk
}

func (s *Stats) add(o Outcome) {
	switch o {
	case Counted:
		s.Counted++
	case Cooldown:
		s.Cooldown++
	case Invisible:
		s.Invisible++
	case Attributed:
		s.Attributed++
	case Duplicate:
		s.Duplicate++
	case Expired:
		s.Expired++
	case TooFast:
		s.TooFast++
	case NoImpression:
		s.NoImpression++
	case CrossTalk:
		s.CrossTalk++
	}
}

// pairKey (user, ad) 对。
type pairKey struct {
	user string
	ad   string
}

// pairState 每对只保留常数个状态。
type pairState struct {
	watermark    int64 // 事件水位：见过的最大 t
	hasWatermark bool
	anchor       int64 // 锚点：最近一次已计数曝光的时刻
	hasAnchor    bool
	attributed   bool  // 锚点是否已被归因
	lockUntil    int64 // 锁定截止时刻 e
	hasLock      bool
}

// userState 每个 user 只保留已归因点击时刻的最大值 u。
type userState struct {
	u    int64
	hasU bool
}

// Reconciler 曝光点击对账器。Submit 与 Stats 可并发调用，
// 结果等价于某个串行顺序，一批事件原子生效。
type Reconciler struct {
	mu                    sync.Mutex
	v, c, a, tmin, lk, tu int64
	pairs                 map[pairKey]*pairState
	users                 map[string]*userState
	stats                 Stats
}

// New 构造对账器。所有参数单位为毫秒，取值 [0, 10^9]，越界返回 ErrInvalidParam。
//
//	v    可见时长门槛：v < V 的曝光记为不可见
//	c    同广告冷却：t-锚点 < C 的曝光记为冷却
//	a    归因窗口：g > A 的点击记为已过期（恰等于 A 有效）
//	tmin 最短点击间隔：g < Tmin 的点击记为过快
//	lk   归因后锁定期：归因后 e = t+Lk，t < e 的曝光记为冷却
//	tu   用户级归因间隔：t-u < Tu 的点击记为串扰
func New(v, c, a, tmin, lk, tu int64) (*Reconciler, error) {
	names := []string{"V", "C", "A", "Tmin", "Lk", "Tu"}
	params := []int64{v, c, a, tmin, lk, tu}
	for i, p := range params {
		if p < 0 || p > maxParam {
			return nil, fmt.Errorf("%w: 构造参数 %s=%d 越界 [0, 1e9]", ErrInvalidParam, names[i], p)
		}
	}
	return &Reconciler{
		v: v, c: c, a: a, tmin: tmin, lk: lk, tu: tu,
		pairs: make(map[pairKey]*pairState),
		users: make(map[string]*userState),
	}, nil
}

// Submit 提交一批事件，返回与排序后事件一一对应的结论序列。
// 批内事件先按 (t 升序，同刻曝光先于点击，再按输入次序) 稳定排序再依次处理。
// 参数非法或乱序时整批拒绝（只报第一个原因），不改变任何状态与计数。
func (r *Reconciler) Submit(events []Event) ([]Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range events {
		if err := validateEvent(events[i]); err != nil {
			return nil, err
		}
	}

	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].T != sorted[j].T {
			return sorted[i].T < sorted[j].T
		}
		return sorted[i].Kind < sorted[j].Kind
	})

	// 乱序预检：批内已按 t 升序，只需对每个事件比较其对的当前水位，
	// 预检不修改任何状态，失败时整批拒绝。
	for _, ev := range sorted {
		ps := r.pairs[pairKey{ev.User, ev.Ad}]
		if ps != nil && ps.hasWatermark && ev.T < ps.watermark {
			return nil, fmt.Errorf("%w: (%s,%s) 事件 t=%d 小于水位 %d",
				ErrOutOfOrder, ev.User, ev.Ad, ev.T, ps.watermark)
		}
	}

	outcomes := make([]Outcome, len(sorted))
	for i, ev := range sorted {
		outcomes[i] = r.processLocked(ev)
	}
	return outcomes, nil
}

// Stats 返回九类结论的当前计数快照。
func (r *Reconciler) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func validateEvent(ev Event) error {
	if ev.Kind != Impression && ev.Kind != Click {
		return fmt.Errorf("%w: 未知事件类型 %d", ErrInvalidParam, int(ev.Kind))
	}
	if ev.User == "" || ev.Ad == "" {
		return fmt.Errorf("%w: user 与 ad 均须非空", ErrInvalidParam)
	}
	if ev.T < 0 || ev.T > maxTime {
		return fmt.Errorf("%w: t=%d 越界 [0, 1e15]", ErrInvalidParam, ev.T)
	}
	if ev.Kind == Impression && (ev.V < 0 || ev.V > maxParam) {
		return fmt.Errorf("%w: v=%d 越界 [0, 1e9]", ErrInvalidParam, ev.V)
	}
	return nil
}

// processLocked 处理单个已排序事件，更新状态与计数。
func (r *Reconciler) processLocked(ev Event) Outcome {
	key := pairKey{ev.User, ev.Ad}
	ps := r.pairs[key]
	if ps == nil {
		ps = &pairState{}
		r.pairs[key] = ps
	}
	if !ps.hasWatermark || ev.T > ps.watermark {
		ps.watermark = ev.T
		ps.hasWatermark = true
	}

	var o Outcome
	if ev.Kind == Impression {
		o = r.impression(ps, ev)
	} else {
		o = r.click(ps, ev)
	}
	r.stats.add(o)
	return o
}

// impression 处理曝光：不可见与冷却都不改锚点。
func (r *Reconciler) impression(ps *pairState, ev Event) Outcome {
	if ev.V < r.v {
		return Invisible
	}
	if ps.hasAnchor && ev.T-ps.anchor < r.c {
		return Cooldown
	}
	if ps.hasLock && ev.T < ps.lockUntil {
		return Cooldown
	}
	ps.anchor = ev.T
	ps.hasAnchor = true
	ps.attributed = false
	return Counted
}

// click 处理点击：总是归给最近一次已计数曝光（锚点），按
// 已过期 -> 过快 -> 重复 -> 串扰 的次序只报第一个原因。
func (r *Reconciler) click(ps *pairState, ev Event) Outcome {
	if !ps.hasAnchor {
		return NoImpression
	}
	g := ev.T - ps.anchor
	if g > r.a {
		return Expired
	}
	if g < r.tmin {
		return TooFast
	}
	if ps.attributed {
		return Duplicate
	}
	us := r.users[ev.User]
	if us != nil && us.hasU && ev.T-us.u < r.tu {
		return CrossTalk
	}
	ps.attributed = true
	ps.lockUntil = ev.T + r.lk
	ps.hasLock = true
	if us == nil {
		us = &userState{}
		r.users[ev.User] = us
	}
	if !us.hasU || ev.T > us.u {
		us.u = ev.T
	}
	us.hasU = true
	return Attributed
}
