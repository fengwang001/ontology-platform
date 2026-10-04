// Package access 是申请、授权与回收的并发安全编排入口。
package access

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/grant"
	"ontology/reaper"
	"ontology/request"
)

var (
	ErrClock            = errors.New("clock moved backwards")
	ErrCooldown         = errors.New("subject is in revoke cooldown")
	ErrDuplicatePending = errors.New("duplicate pending request")
	ErrNotPending       = errors.New("request is not pending")
	ErrSelfApprove      = errors.New("approver must not be the applicant")
	ErrNotOwner         = errors.New("actor is not an owner")
	ErrActiveGrant      = errors.New("an active grant already exists")
	ErrLimit            = errors.New("active grant limit reached")
	ErrNoGrant          = errors.New("grant does not exist or is inactive")
	ErrDepth            = errors.New("delegation depth exceeded")
	ErrNotRoot          = errors.New("only root grants can be extended")
	ErrExtendLimit      = errors.New("extension count limit reached")
	ErrTooLong          = errors.New("grant would exceed maximum duration")
)

// Config 是系统构造参数（均为正整数）。
type Config struct {
	P    int64
	M    int
	Lmax int64
	E    int
	Cool int64
}

// System 串行化全部操作并持有时钟与计数器。
type System struct {
	cfg     Config
	mu      sync.RWMutex
	now     int64
	nextID  int64
	owners  map[string]map[string]bool
	tree    *grant.Tree
	book    *request.Book
	reaper  *reaper.Reaper
	touched atomic.Int32
}

// New 构造系统；参数必须全部为正。
func New(cfg Config) *System {
	if !validConfig(cfg) {
		panic("access: invalid config")
	}
	return &System{
		cfg:    cfg,
		owners: make(map[string]map[string]bool),
		tree:   grant.NewTree(),
		book:   request.NewBook(),
		reaper: reaper.New(),
	}
}

func validConfig(cfg Config) bool {
	return cfg.P > 0 && cfg.M > 0 && cfg.Lmax > 0 && cfg.E > 0 && cfg.Cool > 0
}

func bytesKey(b []byte) string { return string(b) }

const maxNow = int64(1_000_000_000_000)

func validTime(now int64) bool { return now >= 0 && now <= maxNow }
func validParty(b []byte) bool { return len(b) > 0 }

func (s *System) commit(now int64) {
	s.reaper.Sweep(s.tree, now)
	s.now = now
}

func (s *System) isOwner(r, who []byte) bool { return s.owners[bytesKey(r)][bytesKey(who)] }

// activeAt 逻辑判定节点在 t 是否处于有效区间且自身与全部祖先未失效。
func (s *System) activeAt(n *grant.Node, t int64) bool {
	for n != nil {
		if !n.Alive() || t < n.Start || t >= n.End {
			return false
		}
		if n.ParentID == 0 {
			return true
		}
		n = s.tree.Get(n.ParentID)
	}
	return false
}

// activeCount 返回主体在 t 时处于有效区间且祖先链完好的授权数。
func (s *System) activeCount(u []byte, t int64) int {
	c := 0
	for _, gid := range s.tree.ByUser(u) {
		if s.activeAt(s.tree.Get(gid), t) {
			c++
		}
	}
	return c
}

// SetOwners 设定数据集属主集合（先执行到期回收）。
func (s *System) SetOwners(r []byte, owners [][]byte, now int64) error {
	if !validParty(r) || !validTime(now) {
		return errors.New("invalid argument")
	}
	for _, o := range owners {
		if !validParty(o) {
			return errors.New("invalid argument")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	set := make(map[string]bool, len(owners))
	for _, o := range owners {
		set[bytesKey(o)] = true
	}
	s.commit(now)
	s.owners[bytesKey(r)] = set
	return nil
}

// Tick 只推进时钟并落地到期回收。
func (s *System) Tick(now int64) error {
	if !validTime(now) {
		return errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	s.commit(now)
	return nil
}

// Request 提交访问申请，返回 Pending 申请。
func (s *System) Request(id string, u, r []byte, dur, now int64) error {
	if id == "" || !validParty(u) || !validParty(r) || !validTime(now) || dur < 1 || dur > s.cfg.Lmax {
		return errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	if s.book.Has(id) {
		return errors.New("request id already exists")
	}
	if s.reaper.Cooling(u, r, now, s.cfg.Cool) {
		return ErrCooldown
	}
	if s.book.HasPending(u, r, now, s.cfg.P) {
		return ErrDuplicatePending
	}
	s.commit(now)
	s.book.Add(id, u, r, dur, now)
	return nil
}

// Approve 批准申请并生成根授权，返回授权编号。
func (s *System) Approve(id string, approver []byte, now int64) (int64, error) {
	if id == "" || !validParty(approver) || !validTime(now) {
		return 0, errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return 0, ErrClock
	}
	q := s.book.Get(id)
	if q == nil {
		return 0, errors.New("request does not exist")
	}
	if q.Status != request.Pending || q.Expired(now, s.cfg.P) {
		return 0, ErrNotPending
	}
	if bytesKey(q.User) == bytesKey(approver) {
		return 0, ErrSelfApprove
	}
	if !s.isOwner(q.Res, approver) {
		return 0, ErrNotOwner
	}
	if nid := s.tree.ActiveID(q.User, q.Res); nid != 0 {
		if n := s.tree.Get(nid); s.activeAt(n, now) {
			return 0, ErrActiveGrant
		}
	}
	if s.activeCount(q.User, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.commit(now)
	s.nextID++
	id2 := s.nextID
	s.tree.AddRoot(id2, q.User, q.Res, now, now+q.Dur)
	s.book.SetStatus(id, request.Approved)
	return id2, nil
}

// Deny 驳回申请。
func (s *System) Deny(id string, approver []byte, now int64) error {
	if id == "" || !validParty(approver) || !validTime(now) {
		return errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	q := s.book.Get(id)
	if q == nil {
		return errors.New("request does not exist")
	}
	if q.Status != request.Pending || q.Expired(now, s.cfg.P) {
		return ErrNotPending
	}
	if bytesKey(q.User) == bytesKey(approver) {
		return ErrSelfApprove
	}
	if !s.isOwner(q.Res, approver) {
		return ErrNotOwner
	}
	s.commit(now)
	s.book.SetStatus(id, request.Denied)
	return nil
}

// Extend 自原 end 起延长有效的根授权。
func (s *System) Extend(grantID int64, extra, now int64) error {
	if grantID < 1 || extra < 1 || !validTime(now) {
		return errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	n := s.tree.Get(grantID)
	if n == nil || !s.activeAt(n, now) {
		return ErrNoGrant
	}
	if n.Depth != 0 {
		return ErrNotRoot
	}
	if n.Exts >= s.cfg.E {
		return ErrExtendLimit
	}
	if n.End+extra-n.Start > s.cfg.Lmax {
		return ErrTooLong
	}
	s.commit(now)
	n.End += extra
	n.Exts++
	return nil
}

// Delegate 把有效授权转授给 to，返回子授权编号。
func (s *System) Delegate(grantID int64, to []byte, dur, now int64) (int64, error) {
	if grantID < 1 || !validParty(to) || dur < 1 || !validTime(now) {
		return 0, errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return 0, ErrClock
	}
	parent := s.tree.Get(grantID)
	if parent == nil || !s.activeAt(parent, now) {
		return 0, ErrNoGrant
	}
	if parent.Depth >= 2 {
		return 0, ErrDepth
	}
	if nid := s.tree.ActiveID(to, parent.Res); nid != 0 {
		if n := s.tree.Get(nid); s.activeAt(n, now) {
			return 0, ErrActiveGrant
		}
	}
	if s.activeCount(to, now) >= s.cfg.M {
		return 0, ErrLimit
	}
	s.commit(now)
	end := now + dur
	if parent.End < end {
		end = parent.End
	}
	s.nextID++
	cid := s.nextID
	s.tree.AddChild(cid, parent, to, now, end)
	return cid, nil
}

// Revoke 由属主撤销授权并级联失效后代。
func (s *System) Revoke(grantID int64, actor []byte, now int64) error {
	if grantID < 1 || !validParty(actor) || !validTime(now) {
		return errors.New("invalid argument")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	n := s.tree.Get(grantID)
	if n == nil || !s.activeAt(n, now) {
		return ErrNoGrant
	}
	if !s.isOwner(n.Res, actor) {
		return ErrNotOwner
	}
	s.commit(now)
	s.reaper.SetCooldown(n.User, n.Res, now+s.cfg.Cool)
	s.reaper.Revoke(s.tree, n, now)
	return nil
}

// Check 只读判定 (u,r) 在 now 是否存在有效授权。
func (s *System) Check(u, r []byte, now int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var touched int32
	if !validParty(u) || !validParty(r) || !validTime(now) || now < s.now {
		s.touched.Store(0)
		return false
	}
	nid := s.tree.ActiveID(u, r)
	if nid == 0 {
		s.touched.Store(0)
		return false
	}
	n := s.tree.Get(nid)
	for depth := 0; n != nil && depth <= 2; depth++ {
		touched++
		if !n.Alive() || now < n.Start || now >= n.End {
			s.touched.Store(touched)
			return false
		}
		if n.ParentID == 0 {
			s.touched.Store(touched)
			return true
		}
		n = s.tree.Get(n.ParentID)
	}
	s.touched.Store(touched)
	return false
}

// Reaped 返回回收日志拷贝。
func (s *System) Reaped() []reaper.Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reaper.Log()
}

// Touched 返回最近一次 Check 读取的授权记录数。
func (s *System) Touched() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.touched.Load())
}

// Grant 按编号取授权（供测试/观测）。
func (s *System) Grant(id int64) *grant.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tree.Get(id)
}
