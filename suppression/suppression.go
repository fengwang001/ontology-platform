package suppression

import (
	"errors"
	"strings"
	"sync"
)

// Cause 表示抑制原因，等级依次升高；CauseDomain 仅用于 IsSuppressed 的判定结果。
type Cause int

const (
	CauseNone      Cause = iota // 无
	CauseSoft                   // 软退信
	CauseUnsub                  // 退订
	CauseHard                   // 硬退信
	CauseComplaint              // 投诉
	CauseDomain                 // 域级抑制（非地址原因等级）
)

func (c Cause) String() string {
	switch c {
	case CauseNone:
		return "none"
	case CauseSoft:
		return "soft"
	case CauseUnsub:
		return "unsub"
	case CauseHard:
		return "hard"
	case CauseComplaint:
		return "complaint"
	case CauseDomain:
		return "domain"
	}
	return "unknown"
}

var (
	// ErrInvalidParam 构造参数越界。
	ErrInvalidParam = errors.New("suppression: invalid parameter")
	// ErrInvalidAddress 地址非法。
	ErrInvalidAddress = errors.New("suppression: invalid address")
	// ErrClockBackwards 时钟回退：now 小于已接受事件的最大 now。
	ErrClockBackwards = errors.New("suppression: clock backwards")
	// ErrRecoveryForbidden RequestConfirm：原因为 complaint，禁止恢复。
	ErrRecoveryForbidden = errors.New("suppression: recovery forbidden (complaint)")
	// ErrRecoveryUnneeded RequestConfirm：原因不是 hard 或 unsub，无需恢复。
	ErrRecoveryUnneeded = errors.New("suppression: recovery unneeded")
	// ErrNoToken Confirm：无令牌（从未请求或已被用掉）。
	ErrNoToken = errors.New("suppression: no token")
	// ErrStaleToken Confirm：编号不是最新。
	ErrStaleToken = errors.New("suppression: token not latest")
	// ErrTokenExpired Confirm：now 不小于 issuedAt+TTL。
	ErrTokenExpired = errors.New("suppression: token expired")
	// ErrConfirmForbidden Confirm：原因为 complaint。
	ErrConfirmForbidden = errors.New("suppression: confirm forbidden (complaint)")
	// ErrConfirmUnneeded Confirm：原因不是 hard 或 unsub。
	ErrConfirmUnneeded = errors.New("suppression: confirm unneeded")
	// ErrTokenPreceded Confirm：令牌签发不晚于最近一次抑制起点，已失效。
	ErrTokenPreceded = errors.New("suppression: token precedes suppression start")
)

// Config 为构造参数，时刻与时长单位一致。
type Config struct {
	SoftThreshold   int64 // S：软退信阈值，1 到 16
	SoftWindow      int64 // W：软退信窗口，1 到 1e9
	SoftTTL         int64 // SoftTTL：软抑制时长，1 到 1e9
	TokenTTL        int64 // TTL：确认令牌有效期，1 到 1e9
	DomainThreshold int64 // Kd：域级硬退信地址数，1 到 1000
	DomainWindow    int64 // Wd：域级窗口，1 到 1e9
	DomainTTL       int64 // DomTTL：域级抑制时长，1 到 1e9
}

func (c Config) valid() bool {
	in := func(v, lo, hi int64) bool { return v >= lo && v <= hi }
	return in(c.SoftThreshold, 1, 16) &&
		in(c.SoftWindow, 1, 1e9) &&
		in(c.SoftTTL, 1, 1e9) &&
		in(c.TokenTTL, 1, 1e9) &&
		in(c.DomainThreshold, 1, 1000) &&
		in(c.DomainWindow, 1, 1e9) &&
		in(c.DomainTTL, 1, 1e9)
}

type token struct {
	seq      uint64
	issuedAt int64
	valid    bool
}

type addrState struct {
	reason      Cause
	since       int64
	softUntil   int64
	log         []int64
	lastHard    int64
	tok         token
	confirmedAt int64
}

type domainState struct {
	domUntil int64
	domSince int64
	addrs    map[string]struct{}
}

// Suppressor 维护地址与域级抑制状态。所有方法可并发调用，
// 结果等价于某个串行顺序。
type Suppressor struct {
	mu   sync.Mutex
	cfg  Config
	now  int64 // 已接受事件的最大 now
	seq  uint64
	addr map[string]*addrState
	dom  map[string]*domainState

	lastDomainScan int // 非导出计数器：最近一次域级统计考察的地址数
}

// New 构造抑制名单；参数越界返回 ErrInvalidParam。
func New(cfg Config) (*Suppressor, error) {
	if !cfg.valid() {
		return nil, ErrInvalidParam
	}
	return &Suppressor{
		cfg:  cfg,
		addr: make(map[string]*addrState),
		dom:  make(map[string]*domainState),
	}, nil
}

// Soft 记录一次软退信。
func (s *Suppressor) Soft(addr string, now int64) error {
	return s.event(addr, now, func(a *addrState, d *domainState) {
		s.applySoft(a, now)
	})
}

// Hard 记录一次硬退信，并做域级聚合。
func (s *Suppressor) Hard(addr string, now int64) error {
	return s.event(addr, now, func(a *addrState, d *domainState) {
		s.applyHard(a, d, now)
	})
}

// Complaint 记录一次投诉。
func (s *Suppressor) Complaint(addr string, now int64) error {
	return s.event(addr, now, func(a *addrState, d *domainState) {
		s.applyComplaint(a, now)
	})
}

// Unsub 记录一次退订。
func (s *Suppressor) Unsub(addr string, now int64) error {
	return s.event(addr, now, func(a *addrState, d *domainState) {
		s.applyUnsub(a, now)
	})
}

// RequestConfirm 为 hard 或 unsub 状态的地址签发确认令牌，返回全局递增编号。
func (s *Suppressor) RequestConfirm(addr string, now int64) (uint64, error) {
	key, err := Normalize(addr)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return 0, ErrClockBackwards
	}
	a := s.addrStateLocked(key)
	s.lazyRecover(a, now)
	if a.reason == CauseComplaint {
		return 0, ErrRecoveryForbidden
	}
	if a.reason != CauseHard && a.reason != CauseUnsub {
		return 0, ErrRecoveryUnneeded
	}
	s.now = now
	s.seq++
	a.tok = token{seq: s.seq, issuedAt: now, valid: true}
	return s.seq, nil
}

// Confirm 按六项顺序检查令牌，通过后恢复地址。
func (s *Suppressor) Confirm(addr string, seq uint64, now int64) error {
	key, err := Normalize(addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClockBackwards
	}
	a := s.addrStateLocked(key)
	s.lazyRecover(a, now)
	switch {
	case !a.tok.valid:
		return ErrNoToken
	case seq != a.tok.seq:
		return ErrStaleToken
	case now >= a.tok.issuedAt+s.cfg.TokenTTL:
		return ErrTokenExpired
	case a.reason == CauseComplaint:
		return ErrConfirmForbidden
	case a.reason != CauseHard && a.reason != CauseUnsub:
		return ErrConfirmUnneeded
	case a.tok.issuedAt <= a.since:
		return ErrTokenPreceded
	}
	s.now = now
	a.reason = CauseNone
	a.log = nil
	a.tok = token{}
	a.confirmedAt = now
	return nil
}

// IsSuppressed 判定地址在 now 时刻是否被抑制。
func (s *Suppressor) IsSuppressed(addr string, now int64) (bool, Cause, error) {
	key, err := Normalize(addr)
	if err != nil {
		return false, CauseNone, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return false, CauseNone, ErrClockBackwards
	}
	a := s.addrStateLocked(key)
	s.lazyRecover(a, now)
	if a.reason != CauseNone {
		return true, a.reason, nil
	}
	d := s.domainOf(key)
	if d.domUntil > now && a.confirmedAt < d.domSince {
		return true, CauseDomain, nil
	}
	return false, CauseNone, nil
}

// event 是 Soft/Hard/Complaint/Unsub 的公共骨架：
// 先校验地址与时钟（被拒则不改任何状态），再惰性恢复并应用事件。
func (s *Suppressor) event(addr string, now int64, apply func(a *addrState, d *domainState)) error {
	key, err := Normalize(addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClockBackwards
	}
	s.now = now
	a := s.addrStateLocked(key)
	s.lazyRecover(a, now)
	apply(a, s.domainOf(key))
	return nil
}

// addrStateLocked 返回规范化地址的状态，不存在则按初值创建。
func (s *Suppressor) addrStateLocked(key string) *addrState {
	a, ok := s.addr[key]
	if !ok {
		a = &addrState{lastHard: -1, confirmedAt: -1}
		s.addr[key] = a
		s.domainOf(key).addrs[key] = struct{}{}
	}
	return a
}

// domainOf 返回规范化地址所属域名的状态，不存在则按初值创建。
func (s *Suppressor) domainOf(key string) *domainState {
	name := key[strings.IndexByte(key, '@')+1:]
	d, ok := s.dom[name]
	if !ok {
		d = &domainState{addrs: make(map[string]struct{})}
		s.dom[name] = d
	}
	return d
}

// lazyRecover 在操作开始时惰性恢复过期的软抑制。
func (s *Suppressor) lazyRecover(a *addrState, now int64) {
	if a.reason == CauseSoft && now >= a.softUntil {
		a.reason = CauseNone
		a.log = nil
	}
}

func (s *Suppressor) applySoft(a *addrState, now int64) {
	if a.reason != CauseNone {
		return
	}
	kept := a.log[:0]
	for _, t := range a.log {
		if t > now-s.cfg.SoftWindow {
			kept = append(kept, t)
		}
	}
	a.log = append(kept, now)
	if int64(len(a.log)) >= s.cfg.SoftThreshold {
		a.reason = CauseSoft
		a.since = now
		a.softUntil = now + s.cfg.SoftTTL
		a.log = nil
	}
}

func (s *Suppressor) applyHard(a *addrState, d *domainState, now int64) {
	a.lastHard = now
	if a.reason < CauseHard {
		a.reason = CauseHard
		a.since = now
		a.log = nil
	}
	scanned := 0
	count := int64(0)
	for key := range d.addrs {
		scanned++
		if s.addr[key].lastHard > now-s.cfg.DomainWindow {
			count++
		}
	}
	s.lastDomainScan = scanned
	if count >= s.cfg.DomainThreshold {
		if d.domUntil <= now {
			d.domSince = now
		}
		if until := now + s.cfg.DomainTTL; until > d.domUntil {
			d.domUntil = until
		}
	}
}

func (s *Suppressor) applyComplaint(a *addrState, now int64) {
	if a.reason < CauseComplaint {
		a.reason = CauseComplaint
		a.since = now
	}
}

func (s *Suppressor) applyUnsub(a *addrState, now int64) {
	if a.reason == CauseNone || a.reason == CauseSoft {
		a.reason = CauseUnsub
		a.since = now
	}
}
