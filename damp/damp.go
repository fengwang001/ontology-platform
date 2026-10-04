// Package damp 实现路由抖动抑制器。
//
// 每条路由的惩罚值按 decay 包逐步整数衰减；惩罚达到 Ps 进入抑制，
// 解除时刻 reuseAt=min(supSince+Tmax, last+j·Δ) 由 reuse 包调度。
// 解除是时间的纯函数：每个被接受的操作（含 Tick）开始先解除全部
// reuseAt≤now 的路由，再执行本操作。所有方法可并发调用，效果等价
// 于某个串行顺序。
package damp

import (
	"errors"
	"fmt"
	"sync"

	"ontology/decay"
	"ontology/reuse"
)

// 可区分错误，用 errors.Is 判定。
var (
	ErrInvalidParam  = errors.New("damp: invalid parameter")
	ErrClockBack     = errors.New("damp: clock regression")
	ErrRouteNotFound = errors.New("damp: route not found")
	ErrPeerNotFound  = errors.New("damp: peer not found")
)

const (
	maxPeer   = 1_000_000
	maxPrefix = 1_000_000_000
	maxNow    = 1_000_000_000_000

	penaltyAttrChange = 500
	penaltyWithdraw   = 1000
)

// Event 报告一条路由解除抑制；Usable 表示解除后该路由在通告中且未被抑制。
type Event struct {
	Peer   int64
	Prefix int64
	At     int64
	Usable bool
}

// record 是路由的惩罚记录，首次受罚时才建立。
type record struct {
	p          int64
	last       int64
	suppressed bool
	supSince   int64
	reuseAt    int64
}

// Dampener 是路由抖动抑制器。
type Dampener struct {
	mu   sync.Mutex
	dec  *decay.Decayer
	ps   int64
	pr   int64
	pmax int64
	tmax int64

	records   map[reuse.Key]*record
	announced map[reuse.Key]uint64
	byPeer    map[int64]map[int64]struct{}
	sched     *reuse.Scheduler
	now       int64
}

// New 校验 1≤Δ≤10^6、1≤num<den≤1000、1≤Pr<Ps≤Pmax≤10^9、1≤Tmax≤10^9。
func New(delta, num, den, ps, pr, pmax, tmax int64) (*Dampener, error) {
	if pr < 1 || pr >= ps || ps > pmax || pmax > 1_000_000_000 {
		return nil, fmt.Errorf("%w: need 1<=Pr<Ps<=Pmax<=1e9, got Pr=%d Ps=%d Pmax=%d",
			ErrInvalidParam, pr, ps, pmax)
	}
	if tmax < 1 || tmax > 1_000_000_000 {
		return nil, fmt.Errorf("%w: Tmax=%d out of [1,1e9]", ErrInvalidParam, tmax)
	}
	dec, err := decay.New(delta, num, den, pmax)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidParam, err)
	}
	return &Dampener{
		dec:       dec,
		ps:        ps,
		pr:        pr,
		pmax:      pmax,
		tmax:      tmax,
		records:   make(map[reuse.Key]*record),
		announced: make(map[reuse.Key]uint64),
		byPeer:    make(map[int64]map[int64]struct{}),
		sched:     reuse.NewScheduler(),
	}, nil
}

func checkKey(peer, prefix int64) error {
	if peer < 1 || peer > maxPeer || prefix < 1 || prefix > maxPrefix {
		return fmt.Errorf("%w: route key (%d,%d)", ErrInvalidParam, peer, prefix)
	}
	return nil
}

func checkPeer(peer int64) error {
	if peer < 1 || peer > maxPeer {
		return fmt.Errorf("%w: peer %d", ErrInvalidParam, peer)
	}
	return nil
}

func checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now %d out of [0,1e12]", ErrInvalidParam, now)
	}
	return nil
}

func (d *Dampener) checkClock(now int64) error {
	if now < d.now {
		return fmt.Errorf("%w: now %d < accepted %d", ErrClockBack, now, d.now)
	}
	return nil
}

// popReuse 解除全部 reuseAt≤now 的抑制并生成事件。
func (d *Dampener) popReuse(now int64) []Event {
	var evs []Event
	for _, it := range d.sched.PopDue(now) {
		rec := d.records[it.Key]
		rec.suppressed = false
		_, ann := d.announced[it.Key]
		evs = append(evs, Event{Peer: it.Key.Peer, Prefix: it.Key.Prefix, At: it.At, Usable: ann})
	}
	return evs
}

// penalize 先结算到 now，再加罚并按规则判定抑制。
func (d *Dampener) penalize(key reuse.Key, amount, now int64) {
	rec := d.records[key]
	if rec == nil {
		rec = &record{last: now}
		d.records[key] = rec
	}
	if k := (now - rec.last) / d.dec.Delta(); k > 0 {
		rec.p = d.dec.Decay(rec.p, k)
		rec.last += k * d.dec.Delta()
	}
	rec.p += amount
	if rec.p > d.pmax {
		rec.p = d.pmax
	}
	if rec.suppressed {
		d.schedule(key, rec) // 抑制中再受罚：重算 reuseAt，supSince 不变
	} else if rec.p >= d.ps {
		rec.suppressed = true
		rec.supSince = now
		d.schedule(key, rec)
	}
}

func (d *Dampener) schedule(key reuse.Key, rec *record) {
	j := d.dec.StepsBelow(rec.p, d.pr)
	rec.reuseAt = min(rec.supSince+d.tmax, rec.last+j*d.dec.Delta())
	d.sched.Upsert(key, rec.reuseAt)
}

// Announce 处理通告：新路由变为通告中不加罚；attr 变化加罚 500；
// attr 相同的重复通告不改状态。
func (d *Dampener) Announce(peer, prefix int64, attr uint64, now int64) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := checkKey(peer, prefix); err != nil {
		return nil, err
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := d.checkClock(now); err != nil {
		return nil, err
	}
	key := reuse.Key{Peer: peer, Prefix: prefix}
	evs := d.popReuse(now)
	old, isAnn := d.announced[key]
	switch {
	case !isAnn:
		d.announced[key] = attr
		set := d.byPeer[peer]
		if set == nil {
			set = make(map[int64]struct{})
			d.byPeer[peer] = set
		}
		set[prefix] = struct{}{}
	case old != attr:
		d.announced[key] = attr
		d.penalize(key, penaltyAttrChange, now)
	}
	d.now = now
	return evs, nil
}

// Withdraw 处理撤销：在通告中则撤销并加罚 1000，否则报路由不存在。
func (d *Dampener) Withdraw(peer, prefix int64, now int64) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := checkKey(peer, prefix); err != nil {
		return nil, err
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := d.checkClock(now); err != nil {
		return nil, err
	}
	key := reuse.Key{Peer: peer, Prefix: prefix}
	if _, ok := d.announced[key]; !ok {
		return nil, fmt.Errorf("%w: (%d,%d)", ErrRouteNotFound, peer, prefix)
	}
	evs := d.popReuse(now)
	delete(d.announced, key)
	delete(d.byPeer[peer], prefix)
	if len(d.byPeer[peer]) == 0 {
		delete(d.byPeer, peer)
	}
	d.penalize(key, penaltyWithdraw, now)
	d.now = now
	return evs, nil
}

// PeerDown 将该邻居所有通告中的路由撤销，不加罚；没有则报邻居不存在。
func (d *Dampener) PeerDown(peer int64, now int64) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := checkPeer(peer); err != nil {
		return nil, err
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := d.checkClock(now); err != nil {
		return nil, err
	}
	set := d.byPeer[peer]
	if len(set) == 0 {
		return nil, fmt.Errorf("%w: %d", ErrPeerNotFound, peer)
	}
	evs := d.popReuse(now)
	for prefix := range set {
		delete(d.announced, reuse.Key{Peer: peer, Prefix: prefix})
	}
	delete(d.byPeer, peer)
	d.now = now
	return evs, nil
}

// Tick 只推进时钟并落到期的解除事件。
func (d *Dampener) Tick(now int64) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := d.checkClock(now); err != nil {
		return nil, err
	}
	evs := d.popReuse(now)
	d.now = now
	return evs, nil
}

// Penalty 只读返回结算到 t 的惩罚值与抑制状态，不推进时钟、不落地状态。
func (d *Dampener) Penalty(peer, prefix int64, t int64) (int64, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := checkKey(peer, prefix); err != nil {
		return 0, false, err
	}
	if err := checkNow(t); err != nil {
		return 0, false, err
	}
	if err := d.checkClock(t); err != nil {
		return 0, false, err
	}
	rec := d.records[reuse.Key{Peer: peer, Prefix: prefix}]
	if rec == nil {
		return 0, false, nil
	}
	p := d.dec.Decay(rec.p, (t-rec.last)/d.dec.Delta())
	suppressed := rec.suppressed && t < rec.reuseAt
	return p, suppressed, nil
}
