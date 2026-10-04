package reaper

import (
	"errors"
	"sync"

	"ontology/grant"
	"ontology/request"
)

var (
	ErrClockRollback    = errors.New("clock moved backwards")
	ErrBadArgument      = errors.New("invalid argument")
	ErrCooldown         = errors.New("subject is in revoke cooldown")
	ErrDuplicateRequest = errors.New("request id already exists")
	ErrDuplicatePending = errors.New("a pending request already exists for subject and dataset")
	ErrRequestNotFound  = errors.New("request not found")
	ErrNotPending       = errors.New("request is not pending")
	ErrSelfApprove      = errors.New("approver must not be the applicant")
	ErrNotOwner         = errors.New("actor is not an owner")
	ErrActiveGrant      = errors.New("an active grant already exists for subject and dataset")
	ErrLimit            = errors.New("subject reached the concurrent grant limit")
	ErrNoGrant          = errors.New("grant not found or no longer active")
	ErrDepth            = errors.New("delegation depth exceeded")
	ErrNotRoot          = errors.New("only root grants can be extended")
	ErrExtendLimit      = errors.New("extension count limit exceeded")
	ErrTooLong          = errors.New("grant would exceed maximum total duration")
)

// Reason 是回收原因。
type Reason string

const (
	Expired  Reason = "Expired"
	Cascaded Reason = "Cascaded"
	Revoked  Reason = "Revoked"
)

// Config 是构造参数，全部为正整数秒或个数。
type Config struct {
	P    int64 // 申请有效期
	M    int   // 每主体同时有效授权数上限
	Lmax int64 // 单个授权总时长上限
	E    int   // 延长次数上限
	Cool int64 // 撤销冷却
}

// Event 记录一个授权的失效。
type Event struct {
	GrantID grant.ID
	Reason  Reason
	At      int64
}

// System 是三个包之上的串行化外观；所有方法可并发调用。
type System struct {
	mu      sync.Mutex
	cfg     Config
	clock   int64
	req     *request.Store
	gr      *grant.Store
	owners  map[string]map[string]struct{}
	cool    map[coolKey]int64
	reaped  []Event
	touched int // Check 读取的授权记录数
}

type coolKey struct {
	u, r string
}

// New 构造系统；任一参数非正即拒绝。
func New(c Config) (*System, error) {
	if c.P <= 0 || c.M <= 0 || c.Lmax <= 0 || c.E <= 0 || c.Cool <= 0 {
		return nil, ErrBadArgument
	}
	return &System{
		cfg:    c,
		req:    request.NewStore(c.P),
		gr:     grant.NewStore(),
		owners: map[string]map[string]struct{}{},
		cool:   map[coolKey]int64{},
	}, nil
}

func nonempty(bs ...[]byte) bool {
	for _, b := range bs {
		if len(b) == 0 {
			return false
		}
	}
	return true
}

func validTime(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (s *System) isOwner(r, who string) bool {
	set := s.owners[r]
	if set == nil {
		return false
	}
	_, ok := set[who]
	return ok
}

// SetOwners 设定数据集 r 的属主集合（整体替换）。
func (s *System) SetOwners(r []byte, owners [][]byte, now int64) error {
	if !nonempty(r) || len(owners) == 0 {
		return ErrBadArgument
	}
	for _, o := range owners {
		if !nonempty(o) {
			return ErrBadArgument
		}
	}
	if !validTime(now) {
		return ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	s.sweep(now)
	s.clock = now
	set := make(map[string]struct{}, len(owners))
	for _, o := range owners {
		set[string(o)] = struct{}{}
	}
	s.owners[string(r)] = set
	return nil
}

// Request 提交访问申请。
func (s *System) Request(id []byte, u, r []byte, dur, now int64) error {
	if !nonempty(id, u, r) || dur < 1 || dur > s.cfg.Lmax || !validTime(now) {
		return ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	key := string(id)
	if s.req.Get(key) != nil {
		return ErrDuplicateRequest
	}
	us, rs := string(u), string(r)
	if until, ok := s.cool[coolKey{us, rs}]; ok && now < until {
		return ErrCooldown
	}
	if s.req.HasPending(us, rs, now) {
		return ErrDuplicatePending
	}
	s.sweep(now)
	s.clock = now
	s.req.Add(key, us, rs, dur, now)
	return nil
}

// Approve 批准申请并生成根授权 [now, now+dur)。
func (s *System) Approve(id, approver []byte, now int64) (grant.ID, error) {
	if !nonempty(id, approver) || !validTime(now) {
		return 0, ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return 0, ErrClockRollback
	}
	q := s.req.Get(string(id))
	if q == nil {
		return 0, ErrRequestNotFound
	}
	if q.Status != request.Pending || q.ExpiredAt(now, s.cfg.P) {
		return 0, ErrNotPending
	}
	ap := string(approver)
	if ap == q.U {
		return 0, ErrSelfApprove
	}
	if !s.isOwner(q.R, ap) {
		return 0, ErrNotOwner
	}
	if _, ok := s.gr.Active(q.U, q.R); ok {
		if s.gr.LiveAt(s.grActive(q.U, q.R), now) != nil {
			return 0, ErrActiveGrant
		}
	}
	if s.gr.LiveCountAt(q.U, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.sweep(now)
	if s.gr.LiveCountAt(q.U, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.clock = now
	s.req.Mark(q, request.Approved)
	g := s.gr.Create(q.U, q.R, now, now+q.Dur, 0, 0)
	return g.ID, nil
}

// Deny 驳回申请。
func (s *System) Deny(id, approver []byte, now int64) error {
	if !nonempty(id, approver) || !validTime(now) {
		return ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	q := s.req.Get(string(id))
	if q == nil {
		return ErrRequestNotFound
	}
	if q.Status != request.Pending || q.ExpiredAt(now, s.cfg.P) {
		return ErrNotPending
	}
	if string(approver) == q.U {
		return ErrSelfApprove
	}
	if !s.isOwner(q.R, string(approver)) {
		return ErrNotOwner
	}
	s.sweep(now)
	s.clock = now
	s.req.Mark(q, request.Denied)
	return nil
}

// Delegate 把访问转授给 to，子区间被父 end 封顶；深度 2 不可再转授。
func (s *System) Delegate(from grant.ID, to []byte, dur, now int64) (grant.ID, error) {
	if !nonempty(to) || dur < 1 || !validTime(now) {
		return 0, ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return 0, ErrClockRollback
	}
	pg := s.gr.LiveAt(from, now)
	if pg == nil {
		return 0, ErrNoGrant
	}
	if pg.Depth >= 2 {
		return 0, ErrDepth
	}
	ts := string(to)
	if id, ok := s.gr.Active(ts, pg.R); ok && s.gr.LiveAt(id, now) != nil {
		return 0, ErrActiveGrant
	}
	if s.gr.LiveCountAt(ts, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.sweep(now)
	if s.gr.LiveAt(from, now) == nil {
		return 0, ErrNoGrant
	}
	if s.gr.LiveCountAt(ts, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.clock = now
	end := now + dur
	if pg.End < end {
		end = pg.End
	}
	g := s.gr.Create(ts, pg.R, now, end, pg.ID, pg.Depth+1)
	return g.ID, nil
}

// Extend 延长有效根授权：newEnd = 旧 end + extra（自原到期时刻起算）。
func (s *System) Extend(id grant.ID, extra, now int64) (newEnd int64, err error) {
	if extra < 1 || !validTime(now) {
		return 0, ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return 0, ErrClockRollback
	}
	g := s.gr.LiveAt(id, now)
	if g == nil {
		return 0, ErrNoGrant
	}
	if g.Parent != 0 {
		return 0, ErrNotRoot
	}
	if g.Extends >= s.cfg.E {
		return 0, ErrExtendLimit
	}
	if g.End+extra-g.Start > s.cfg.Lmax {
		return 0, ErrTooLong
	}
	s.sweep(now)
	if s.gr.LiveAt(id, now) == nil {
		return 0, ErrNoGrant
	}
	s.clock = now
	g.Extends++
	g.End += extra
	s.gr.ChangeEnd(g, g.End)
	return g.End, nil
}

// Revoke 由属主撤销一个有效授权，后代级联失效；仅直接者进入冷却。
func (s *System) Revoke(id grant.ID, actor []byte, now int64) error {
	if !nonempty(actor) || !validTime(now) {
		return ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	g := s.gr.LiveAt(id, now)
	if g == nil {
		return ErrNoGrant
	}
	if !s.isOwner(g.R, string(actor)) {
		return ErrNotOwner
	}
	s.sweep(now)
	g = s.gr.LiveAt(id, now)
	if g == nil {
		return ErrNoGrant
	}
	s.clock = now
	s.gr.MarkDead(g)
	s.reaped = append(s.reaped, Event{g.ID, Revoked, now})
	for _, d := range s.gr.LiveDescendants(g) {
		s.gr.MarkDead(d)
		s.reaped = append(s.reaped, Event{d.ID, Cascaded, now})
	}
	s.cool[coolKey{g.U, g.R}] = now + s.cfg.Cool
	return nil
}

// Tick 只推进时钟并落地到期回收。
func (s *System) Tick(now int64) error {
	if !validTime(now) {
		return ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClockRollback
	}
	s.sweep(now)
	s.clock = now
	return nil
}

// Check 只读判定：不落地、不推进时钟；now 回退报错。touched 记读取记录数（≤3）。
func (s *System) Check(u, r []byte, now int64) (bool, error) {
	if !nonempty(u, r) || !validTime(now) {
		return false, ErrBadArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return false, ErrClockRollback
	}
	id, ok := s.gr.Active(string(u), string(r))
	if !ok {
		s.touched = 0
		return false, nil
	}
	s.touched = 1
	g := s.gr.Get(id)
	if g.Dead || g.End <= now {
		return false, nil
	}
	for g.Parent != 0 {
		p := s.gr.Get(g.Parent)
		s.touched++
		if p == nil || p.Dead || p.End <= now {
			return false, nil
		}
		g = p
	}
	return true, nil
}

// Reaped 返回回收日志快照。
func (s *System) Reaped() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.reaped))
	copy(out, s.reaped)
	return out
}

func (s *System) grActive(u, r string) grant.ID {
	id, _ := s.gr.Active(u, r)
	return id
}

// sweep 落地所有 end≤now 的到期授权及其级联后代。
func (s *System) sweep(now int64) {
	for {
		id, end, ok := s.gr.PopDue(now)
		if !ok {
			return
		}
		g := s.gr.Get(id)
		if g == nil || g.Dead {
			continue
		}
		s.gr.MarkDead(g)
		s.reaped = append(s.reaped, Event{g.ID, Expired, end})
		for _, d := range s.gr.LiveDescendants(g) {
			s.gr.MarkDead(d)
			s.reaped = append(s.reaped, Event{d.ID, Cascaded, end})
		}
	}
}
