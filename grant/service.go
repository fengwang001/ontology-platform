package grant

import (
	"sync"

	"ontology/ledger"
	"ontology/policy"
)

type principal struct {
	role   policy.Role
	scopes []string
}

// Service 是紧急访问授权服务。所有方法可并发调用，
// 内部以一把互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu          sync.Mutex
	clock       int64
	policy      policy.Policy
	policyVer   int64
	principals  map[string]principal
	grants      map[string]*Grant
	byRequester map[string][]*Grant
	led         *ledger.Ledger

	accessChecks int64 // 非导出计数器：Access 累计考察的授权数
}

// NewService 以 p 为版本 1 策略构造服务。
func NewService(p policy.Policy) (*Service, error) {
	if !p.Valid() {
		return nil, ErrParam
	}
	return &Service{
		policy:      p,
		policyVer:   1,
		principals:  make(map[string]principal),
		grants:      make(map[string]*Grant),
		byRequester: make(map[string][]*Grant),
		led:         ledger.New(),
	}, nil
}

// checkClock 校验 now 合法且不回退；成功时推进时钟。
// 调用方须持锁，且须在所有拒绝分支之后调用。
func (s *Service) checkClock(now int64) error {
	if !policy.ValidNow(now) {
		return ErrParam
	}
	if now < s.clock {
		return ErrClock
	}
	return nil
}

// Register 登记主体。拒绝顺序：参数非法、时钟回退、重复。
func (s *Service) Register(now int64, name string, role policy.Role, scopes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !policy.ValidName(name) || !policy.ValidRole(role) ||
		!policy.ValidScopes(role, scopes) {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.principals[name]; ok {
		return ErrDuplicate
	}
	s.clock = now
	s.principals[name] = principal{role: role, scopes: append([]string(nil), scopes...)}
	s.led.Append(now, ledger.KindRegister, name, name, s.policyVer)
	return nil
}

// SetPolicy 更新策略，成功后版本加 1；已有授权不受影响。
// 拒绝顺序：参数非法、时钟回退。
func (s *Service) SetPolicy(now int64, p policy.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.Valid() {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.clock = now
	s.policy = p
	s.policyVer++
	s.led.Append(now, ledger.KindSetPolicy, "", "", s.policyVer)
	return nil
}

// Request 申请紧急授权，成功即生效。拒绝顺序：参数非法、时钟回退、
// id 重复、申请人未登记、被锁定、并发授权过多。
func (s *Service) Request(now int64, id, requester, resource string, d int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || !policy.ValidPath(resource) || d < 1 || d > s.policy.Dmax {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.grants[id]; ok {
		return ErrDuplicate
	}
	if _, ok := s.principals[requester]; !ok {
		return ErrNotFound
	}
	own := s.byRequester[requester]
	for _, g := range own {
		if g.lockedAt(now) {
			return ErrLocked
		}
	}
	var active int64
	for _, g := range own {
		if g.activeAt(now) {
			active++
		}
	}
	if active >= s.policy.Cmax {
		return ErrTooMany
	}
	s.clock = now
	g := &Grant{
		ID: id, Requester: requester, Resource: resource,
		Start: now, NominalEnd: now + d,
		Rw: s.policy.Rw, Lk: s.policy.Lk, PolicyVer: s.policyVer,
		ApprovedAt: -1, RejectedAt: -1,
	}
	s.grants[id] = g
	s.byRequester[requester] = append(own, g)
	s.led.Append(now, ledger.KindRequest, id, requester, s.policyVer)
	return nil
}

// Ledger 返回 Seq 大于 afterSeq 的账本条目副本。
func (s *Service) Ledger(afterSeq int64) []ledger.Entry {
	return s.led.After(afterSeq)
}
