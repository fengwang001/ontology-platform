package attribution

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Event 表示一个待对账事件：曝光或点击。
type Event struct {
	Kind EventKind
	User string
	Ad   string
	T    int64
	V    int64
}

// EventKind 是事件种类。
type EventKind int

const (
	Impression EventKind = iota
	Click
)

// Outcome 是一个事件的归属结论。
type Outcome int

const (
	Counted Outcome = iota
	Cooldown
	NotVisible
	Attributed
	Duplicate
	Expired
	TooFast
	NoImpression
	CrossTalk
)

func (o Outcome) String() string {
	switch o {
	case Counted:
		return "已计数"
	case Cooldown:
		return "冷却"
	case NotVisible:
		return "不可见"
	case Attributed:
		return "归因"
	case Duplicate:
		return "重复"
	case Expired:
		return "已过期"
	case TooFast:
		return "过快"
	case NoImpression:
		return "无曝光"
	case CrossTalk:
		return "串扰"
	default:
		return "未知"
	}
}

// Result 是单个事件处理后的结论及其判定依据。
type Result struct {
	Outcome Outcome
	Reason  string
}

// Config 为对账器构造参数，单位均为毫秒。
type Config struct {
	V    int64
	C    int64
	A    int64
	Tmin int64
	Lk   int64
	Tu   int64
}

// Stats 为九类结论的计数。
type Stats struct {
	Counted      int64
	Cooldown     int64
	NotVisible   int64
	Attributed   int64
	Duplicate    int64
	Expired      int64
	TooFast      int64
	NoImpression int64
	CrossTalk    int64
}

const maxTime = int64(1_000_000_000_000_000)
const maxDuration = int64(1_000_000_000)

// ErrInvalid 表示整批因参数非法被拒绝（构造参数越界、字段为空或取值越界）。
var ErrInvalid = errors.New("attribution: invalid argument")

// ErrOutOfOrder 表示整批因乱序被拒绝（排序后存在 t 小于所属对事件水位的事件）。
var ErrOutOfOrder = errors.New("attribution: out of order batch")

// NewImpression 构造一个曝光事件。
func NewImpression(user, ad string, t, v int64) Event {
	return Event{Kind: Impression, User: user, Ad: ad, T: t, V: v}
}

// NewClick 构造一个点击事件。
func NewClick(user, ad string, t int64) Event {
	return Event{Kind: Click, User: user, Ad: ad, T: t}
}

type pairKey struct {
	user string
	ad   string
}

type anchor struct {
	t       int64
	used    bool
	present bool
}

type pairState struct {
	watermark    int64
	hasWatermark bool
	anchor       anchor
	lockUntil    int64
	hasLockUntil bool
}

// Reconciler 是并发安全的曝光点击对账器。
type Reconciler struct {
	mu    sync.Mutex
	cfg   Config
	pairs map[pairKey]*pairState
	userU map[string]int64
	hasU  map[string]bool
	stats Stats
}

// New 构造一个对账器；参数非法时返回错误。
func New(cfg Config) (*Reconciler, error) {
	if !validParam(cfg.V) || !validParam(cfg.C) || !validParam(cfg.A) ||
		!validParam(cfg.Tmin) || !validParam(cfg.Lk) || !validParam(cfg.Tu) {
		return nil, fmt.Errorf("%w: config duration out of [0,1e9]", ErrInvalid)
	}
	return &Reconciler{
		cfg:   cfg,
		pairs: make(map[pairKey]*pairState),
		userU: make(map[string]int64),
		hasU:  make(map[string]bool),
	}, nil
}

// Submit 原子地处理一批事件；整批非法时不改变任何状态。
func (r *Reconciler) Submit(events []Event) (results []Result, err error) {
	if err := validateEvents(events); err != nil {
		return nil, err
	}

	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := events[order[i]], events[order[j]]
		if a.T != b.T {
			return a.T < b.T
		}
		if a.Kind != b.Kind {
			return a.Kind == Impression
		}
		return false
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	// 第一阶段：在工作副本上校验乱序并完整推演；任何拒绝都不触碰正式状态。
	work := make(map[pairKey]*pairState, len(events))
	workUsers := make(map[string]userWork)
	for _, idx := range order {
		e := events[idx]
		key := pairKey{e.User, e.Ad}
		st := work[key]
		if st == nil {
			st = r.clonePair(key)
			work[key] = st
		}
		if _, ok := workUsers[e.User]; !ok && r.hasU[e.User] {
			workUsers[e.User] = userWork{u: r.userU[e.User], hasU: true}
		}
		if st.hasWatermark && e.T < st.watermark {
			return nil, fmt.Errorf("%w: event t=%d for (user=%q,ad=%q) below watermark %d",
				ErrOutOfOrder, e.T, e.User, e.Ad, st.watermark)
		}
	}

	orderedResults := make([]Result, len(events))
	for _, idx := range order {
		e := events[idx]
		key := pairKey{e.User, e.Ad}
		st := work[key]
		var res Result
		if e.Kind == Impression {
			res = r.processImpression(e, st)
		} else {
			res = r.processClick(e, st, workUsers)
		}
		st.watermark = e.T
		st.hasWatermark = true
		orderedResults[idx] = res
		r.bump(res.Outcome)
	}

	// 第二阶段：全部事件合法后一次性提交，保证批的原子可见性。
	for key, st := range work {
		r.pairs[key] = st
	}
	for user, uw := range workUsers {
		if uw.hasU {
			r.userU[user] = uw.u
			r.hasU[user] = true
		}
	}
	return orderedResults, nil
}

func validateEvents(events []Event) error {
	for i := range events {
		e := &events[i]
		if e.User == "" || e.Ad == "" {
			return fmt.Errorf("%w: empty user or ad at index %d", ErrInvalid, i)
		}
		if e.T < 0 || e.T > maxTime {
			return fmt.Errorf("%w: t out of [0,1e15] at index %d", ErrInvalid, i)
		}
		if e.Kind != Impression && e.Kind != Click {
			return fmt.Errorf("%w: unknown event kind at index %d", ErrInvalid, i)
		}
		if e.Kind == Impression && (e.V < 0 || e.V > maxDuration) {
			return fmt.Errorf("%w: v out of [0,1e9] at index %d", ErrInvalid, i)
		}
	}
	return nil
}

// Stats 返回当前九类结论的计数。
func (r *Reconciler) Stats() (s Stats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func validParam(v int64) bool {
	return v >= 0 && v <= maxDuration
}

func (r *Reconciler) clonePair(key pairKey) *pairState {
	if cur := r.pairs[key]; cur != nil {
		cp := *cur
		return &cp
	}
	return &pairState{}
}

type userWork struct {
	u    int64
	hasU bool
}

func (r *Reconciler) processImpression(e Event, st *pairState) Result {
	if e.V < r.cfg.V {
		return Result{NotVisible, fmt.Sprintf(
			"曝光 t=%d v=%d 小于可见门槛 V=%d，记为不可见，锚点不变", e.T, e.V, r.cfg.V)}
	}
	if st.anchor.present && e.T-st.anchor.t < r.cfg.C {
		return Result{Cooldown, fmt.Sprintf(
			"曝光 t=%d 距锚点 %d 的间隔 %d 小于同广告冷却 C=%d，记为冷却，锚点不变",
			e.T, st.anchor.t, e.T-st.anchor.t, r.cfg.C)}
	}
	if st.hasLockUntil && e.T < st.lockUntil {
		return Result{Cooldown, fmt.Sprintf(
			"曝光 t=%d 早于归因后锁定截止 e=%d，记为冷却，锚点不变", e.T, st.lockUntil)}
	}
	st.anchor = anchor{t: e.T, used: false, present: true}
	return Result{Counted, fmt.Sprintf(
		"曝光 t=%d v=%d 满足门槛且未冷却/未锁定，记为已计数，锚点更新为 %d（未归因）",
		e.T, e.V, e.T)}
}

func (r *Reconciler) processClick(e Event, st *pairState, workUsers map[string]userWork) Result {
	if !st.anchor.present {
		return Result{NoImpression, fmt.Sprintf(
			"点击 t=%d 时该 (user,ad) 对不存在已计数曝光锚点，记为无曝光", e.T)}
	}
	g := e.T - st.anchor.t
	if g > r.cfg.A {
		return Result{Expired, fmt.Sprintf(
			"点击 t=%d 距锚点 %d 的间隔 g=%d 大于归因窗口 A=%d，记为已过期",
			e.T, st.anchor.t, g, r.cfg.A)}
	}
	if g < r.cfg.Tmin {
		return Result{TooFast, fmt.Sprintf(
			"点击 t=%d 距锚点 %d 的间隔 g=%d 小于最短点击间隔 Tmin=%d，记为过快，状态不变",
			e.T, st.anchor.t, g, r.cfg.Tmin)}
	}
	if st.anchor.used {
		return Result{Duplicate, fmt.Sprintf(
			"点击 t=%d 指向的锚点 %d 已被归因，记为重复，状态不变", e.T, st.anchor.t)}
	}
	if r.crossTalk(e.User, e.T, workUsers) {
		u := r.userMaxU(e.User, workUsers)
		return Result{CrossTalk, fmt.Sprintf(
			"点击 t=%d 距该 user 最近归因时刻 u=%d 的差 %d 小于用户级归因间隔 Tu=%d，记为串扰，状态不变",
			e.T, u, e.T-u, r.cfg.Tu)}
	}
	st.anchor.used = true
	st.hasLockUntil = true
	st.lockUntil = e.T + r.cfg.Lk
	uw := workUsers[e.User]
	if !uw.hasU || e.T > uw.u {
		uw.u = e.T
		uw.hasU = true
		workUsers[e.User] = uw
	}
	return Result{Attributed, fmt.Sprintf(
		"点击 t=%d 归因给锚点曝光 %d（g=%d），锚点标为已归因，锁定截止 e=%d，用户 u=%d",
		e.T, st.anchor.t, g, st.lockUntil, e.T)}
}

func (r *Reconciler) userMaxU(user string, workUsers map[string]userWork) int64 {
	if uw, ok := workUsers[user]; ok && uw.hasU {
		return uw.u
	}
	if r.hasU[user] {
		return r.userU[user]
	}
	return 0
}

func (r *Reconciler) crossTalk(user string, t int64, workUsers map[string]userWork) bool {
	if r.cfg.Tu == 0 {
		return false
	}
	var u int64
	if uw, ok := workUsers[user]; ok && uw.hasU {
		u = uw.u
	} else if r.hasU[user] {
		u = r.userU[user]
	} else {
		return false
	}
	return t-u < r.cfg.Tu
}

func (r *Reconciler) bump(o Outcome) {
	switch o {
	case Counted:
		r.stats.Counted++
	case Cooldown:
		r.stats.Cooldown++
	case NotVisible:
		r.stats.NotVisible++
	case Attributed:
		r.stats.Attributed++
	case Duplicate:
		r.stats.Duplicate++
	case Expired:
		r.stats.Expired++
	case TooFast:
		r.stats.TooFast++
	case NoImpression:
		r.stats.NoImpression++
	case CrossTalk:
		r.stats.CrossTalk++
	}
}
