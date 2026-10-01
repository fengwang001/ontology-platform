package vegas

import (
	"container/list"
	"errors"
	"sync"
)

// Result 是归还令牌时给出的请求结果。
type Result int

const (
	// Success 表示成功样本，参与滑动最小时延窗口与 Vegas 排队估计。
	Success Result = iota
	// Dropped 表示请求被丢弃，触发一次带冷却的降级。
	Dropped
	// Ignored 表示忽略结果，仅归还在途名额。
	Ignored
)

var (
	ErrInvalidConfig = errors.New("vegas: invalid config")
	ErrInvalidResult = errors.New("vegas: invalid result")
	ErrInvalidTime   = errors.New("vegas: invalid time")
	ErrClockRewind   = errors.New("vegas: clock rewind")
	ErrAtCapacity    = errors.New("vegas: at capacity")
	ErrTokenTimedOut = errors.New("vegas: token timed out")
	ErrInvalidToken  = errors.New("vegas: invalid token")
	ErrInvalidRTT    = errors.New("vegas: invalid rtt")
)

// Token 是一次成功放行的凭证。
type Token struct {
	Seq       int64
	W         int64
	ExpiresAt int64
}

// Config 是限流器的构造参数。
type Config struct {
	L0, Lmin, Lmax    int64
	Alpha, Beta       int64
	Tmo, Cooldown, Wm int64
}

const (
	maxTime     int64 = 1_000_000_000_000_000
	maxLimit    int64 = 1_000_000
	maxTimeout  int64 = 1_000_000_000
	maxWindow   int64 = 1_000_000_000
	maxCooldown int64 = 1_000_000_000
	maxRTT      int64 = 1_000_000_000_000
)

type tokenRec struct {
	w         int64
	expiresAt int64
	done      bool
}

type sample struct {
	expire int64
	rtt    int64
}

// Limiter 是带超时回收、丢弃降级冷却与滑动最小时延窗口的 Vegas 限流器。
// 所有方法均持有同一把互斥锁串行执行，并发调用等价于某个合法的串行顺序。
type Limiter struct {
	mu sync.Mutex

	lmin, lmax     int64
	alpha, beta    int64
	tmo, cd, wm    int64
	l              int64
	n              int64
	hasLastCut     bool
	lastCut        int64
	maxNow         int64
	nextSeq        int64
	tokens         []tokenRec
	reapScan       int64
	timedOut       map[int64]struct{}
	window         *list.List
	minRTT         int64
	tokenExamined  int64
	windowExamined int64
	grantedTotal   int64
	successTotal   int64
}

// New 构造一个限流器；配置非法时整体拒绝并返回 ErrInvalidConfig。
func New(c Config) (*Limiter, error) {
	if c.Lmin < 1 {
		return nil, ErrInvalidConfig
	}
	if c.Lmax < c.Lmin || c.Lmax > maxLimit {
		return nil, ErrInvalidConfig
	}
	if c.L0 < c.Lmin || c.L0 > c.Lmax {
		return nil, ErrInvalidConfig
	}
	if c.Alpha < 0 {
		return nil, ErrInvalidConfig
	}
	if c.Beta <= c.Alpha {
		return nil, ErrInvalidConfig
	}
	if c.Tmo < 1 || c.Tmo > maxTimeout {
		return nil, ErrInvalidConfig
	}
	if c.Wm < 1 || c.Wm > maxWindow {
		return nil, ErrInvalidConfig
	}
	if c.Cooldown < 0 || c.Cooldown > maxCooldown {
		return nil, ErrInvalidConfig
	}
	return &Limiter{
		lmin:     c.Lmin,
		lmax:     c.Lmax,
		alpha:    c.Alpha,
		beta:     c.Beta,
		tmo:      c.Tmo,
		cd:       c.Cooldown,
		wm:       c.Wm,
		l:        c.L0,
		nextSeq:  1,
		reapScan: 1,
		timedOut: make(map[int64]struct{}),
		window:   list.New(),
	}, nil
}

// cut 在冷却允许时把上限收缩 10%（向下取整，不低于 Lmin）并刷新冷却起点。
func (l *Limiter) cut(now int64) {
	if !l.hasLastCut || now >= l.lastCut+l.cd {
		l.l = max(l.lmin, l.l*9/10)
		l.lastCut = now
		l.hasLastCut = true
	}
}

// reap 按序号升序回收所有超时时刻不大于 now 的未归还令牌；
// 每个被回收令牌随后触发一次 cut。超时时刻随序号单调不减，
// 故只需查看队头一次；每个令牌在其生命周期内至多被处理一次。
func (l *Limiter) reap(now int64) {
	for l.reapScan < l.nextSeq {
		rec := &l.tokens[l.reapScan-1]
		if !rec.done && rec.expiresAt > now {
			return
		}
		seq := l.reapScan
		l.reapScan++
		l.tokenExamined++
		if rec.done {
			continue
		}
		rec.done = true
		l.timedOut[seq] = struct{}{}
		l.n--
		l.cut(now)
	}
}

// addSample 把 (now, rtt) 放入滑动窗口并返回窗口内最小时延。
// 窗口保留 expire=now+Wm 大于当前 now 的样本。
// 单调双端队列中每个样本至多被考察（移除）一次。
func (l *Limiter) addSample(now, rtt int64) int64 {
	for l.window.Len() > 0 {
		front := l.window.Front().Value.(sample)
		if front.expire > now {
			break
		}
		l.window.Remove(l.window.Front())
		l.windowExamined++
	}
	cur := sample{expire: now + l.wm, rtt: rtt}
	for l.window.Len() > 0 {
		back := l.window.Back().Value.(sample)
		if back.rtt < cur.rtt {
			break
		}
		l.window.Remove(l.window.Back())
		l.windowExamined++
	}
	l.window.PushBack(cur)
	l.minRTT = l.window.Front().Value.(sample).rtt
	return l.minRTT
}

// checkClock 执行三类操作前校验；通过后推进最大时刻。
func (l *Limiter) checkClock(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < l.maxNow {
		return ErrClockRewind
	}
	l.maxNow = now
	return nil
}

// Acquire 先回收超时令牌，再在 n<L 时放行，否则返回 ErrAtCapacity。
func (l *Limiter) Acquire(now int64) (Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return Token{}, err
	}
	l.reap(now)
	if l.n >= l.l {
		return Token{}, ErrAtCapacity
	}
	l.n++
	seq := l.nextSeq
	l.nextSeq++
	l.grantedTotal++
	l.tokens = append(l.tokens, tokenRec{w: l.n, expiresAt: now + l.tmo})
	return Token{Seq: seq, W: l.n, ExpiresAt: now + l.tmo}, nil
}

// Release 归还令牌。忽略结果只归还名额；丢弃触发一次冷却降级；
// 成功样本进入滑动窗口并按 Vegas 排队估计调整上限。
func (l *Limiter) Release(seq int64, result Result, rtt, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if result != Success && result != Dropped && result != Ignored {
		return ErrInvalidResult
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	l.reap(now)
	if _, ok := l.timedOut[seq]; ok {
		return ErrTokenTimedOut
	}
	if seq < 1 || seq >= l.nextSeq {
		return ErrInvalidToken
	}
	rec := &l.tokens[seq-1]
	if rec.done {
		return ErrInvalidToken
	}
	if result == Success && (rtt < 1 || rtt > maxRTT) {
		return ErrInvalidRTT
	}
	rec.done = true
	l.n--
	switch result {
	case Ignored:
		return nil
	case Dropped:
		l.cut(now)
		return nil
	case Success:
		l.successTotal++
		m := l.addSample(now, rtt)
		if rec.w*2 < l.l {
			return nil
		}
		q := (l.l*(rtt-m) + rtt - 1) / rtt
		switch {
		case q < l.alpha:
			if l.l < l.lmax {
				l.l++
			}
		case q > l.beta:
			if l.l > l.lmin {
				l.l--
			}
		}
		return nil
	}
	return nil
}

// L 返回当前上限。
func (l *Limiter) L() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.l
}

// N 返回当前在途数。
func (l *Limiter) N() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// MinRTT 返回滑动窗口当前最小时延；窗口为空时为 0。
func (l *Limiter) MinRTT() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.minRTT
}

// Examined 返回累计考察的令牌数与窗口样本数（摊销复杂度自证用，非导出语义）。
func (l *Limiter) examined() (tokens, samples int64) {
	return l.tokenExamined, l.windowExamined
}

// totals 返回累计放行令牌数与成功样本数。
func (l *Limiter) totals() (granted, success int64) {
	return l.grantedTotal, l.successTotal
}
