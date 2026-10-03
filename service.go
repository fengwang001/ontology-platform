// Package ontology 提供带事后评审的紧急访问授权服务。
package ontology

import (
	"errors"
	"sync"

	"ontology/grant"
	"ontology/ledger"
	"ontology/policy"
)

var (
	ErrInvalidParam      = errors.New("参数非法")
	ErrClockRegression   = errors.New("时钟回退")
	ErrDuplicate         = errors.New("重复")
	ErrNotRegistered     = errors.New("未登记")
	ErrLocked            = errors.New("被锁定")
	ErrTooManyConcurrent = errors.New("并发授权过多")
	ErrGrantNotFound     = errors.New("授权不存在")
	ErrSelfReview        = errors.New("自评")
	ErrPermission        = errors.New("权限不足")
	ErrAlreadyDecided    = errors.New("已决")
	ErrOverdue           = errors.New("逾期")
	ErrFutureTime        = errors.New("未来时刻")
)

type actor struct {
	role   policy.Role
	scopes []string
}

// Service 是紧急访问授权服务，所有方法可并发调用。
type Service struct {
	mu           sync.Mutex
	clock        int64
	pol          policy.Policy
	ver          int64
	actors       map[string]actor
	grants       map[string]*grant.Grant
	byRequester  map[string][]*grant.Grant
	book         ledger.Ledger
	accessChecks int64
}

// New 以初始策略（版本 1）构造服务。
func New(p policy.Policy) (*Service, error) {
	if !policy.Valid(p) {
		return nil, ErrInvalidParam
	}
	return &Service{
		pol:         p,
		ver:         1,
		actors:      make(map[string]actor),
		grants:      make(map[string]*grant.Grant),
		byRequester: make(map[string][]*grant.Grant),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= policy.MaxTime }

// Register 登记主体。
func (s *Service) Register(now int64, name string, role policy.Role, scopes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || len(name) == 0 || len(name) > 64 || !policy.ValidRole(role) {
		return ErrInvalidParam
	}
	if role == policy.Manager {
		if len(scopes) < 1 || len(scopes) > 8 {
			return ErrInvalidParam
		}
	} else if len(scopes) != 0 {
		return ErrInvalidParam
	}
	for _, sc := range scopes {
		if !policy.ValidPath(sc) {
			return ErrInvalidParam
		}
	}
	if now < s.clock {
		return ErrClockRegression
	}
	if _, ok := s.actors[name]; ok {
		return ErrDuplicate
	}
	s.actors[name] = actor{role: role, scopes: append([]string(nil), scopes...)}
	s.clock = now
	s.book.Append(now, ledger.KindRegister, name, name, s.ver)
	return nil
}

// SetPolicy 更新策略，成功后版本加 1。
func (s *Service) SetPolicy(now int64, p policy.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !policy.Valid(p) {
		return ErrInvalidParam
	}
	if now < s.clock {
		return ErrClockRegression
	}
	s.pol = p
	s.ver++
	s.clock = now
	s.book.Append(now, ledger.KindSetPolicy, "", "", s.ver)
	return nil
}

// Request 申请紧急访问授权，成功即生效。
func (s *Service) Request(now int64, id, requester, resource string, d int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || id == "" || !policy.ValidPath(resource) ||
		d < 1 || d > s.pol.Dmax {
		return ErrInvalidParam
	}
	if now < s.clock {
		return ErrClockRegression
	}
	if _, ok := s.grants[id]; ok {
		return ErrDuplicate
	}
	if _, ok := s.actors[requester]; !ok {
		return ErrNotRegistered
	}
	own := s.byRequester[requester]
	for _, g := range own {
		if ls, ok := g.LockStart(); ok && ls <= now && now < ls+g.Lk {
			return ErrLocked
		}
	}
	var active int64
	for _, g := range own {
		if g.Start <= now && now < g.EffEnd() {
			active++
		}
	}
	if active >= s.pol.Cmax {
		return ErrTooManyConcurrent
	}
	g := grant.New(id, requester, resource, now, d, s.pol.Rw, s.pol.Lk, s.ver)
	s.grants[id] = g
	s.byRequester[requester] = append(own, g)
	s.clock = now
	s.book.Append(now, ledger.KindRequest, id, requester, g.PolicyVer)
	return nil
}

// Access 判定 principal 在时刻 t 对 resource 是否有有效授权。
func (s *Service) Access(principal, resource string, t int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !policy.ValidPath(resource) {
		return false, ErrInvalidParam
	}
	if t < 0 || t > s.clock {
		return false, ErrFutureTime
	}
	if _, ok := s.actors[principal]; !ok {
		return false, nil
	}
	for _, g := range s.byRequester[principal] {
		s.accessChecks++
		if policy.Covers(g.Resource, resource) && g.Start <= t && t < g.EffEnd() {
			return true, nil
		}
	}
	return false, nil
}

// Ledger 返回 Seq 大于 afterSeq 的账本条目副本。
func (s *Service) Ledger(afterSeq int64) []ledger.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.book.After(afterSeq)
}

// Clock 返回当前时钟（已接受的最大 now）。
func (s *Service) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}
