package cred

import "sync"

const (
	maxGrace   = 10_000_000
	maxNow     = 1_000_000_000_000
	maxSigners = 2
)

type status int

const (
	statusActive status = iota
	statusRetiring
	statusDisabled
)

var (
	ErrInvalidParam = errInvalidParam{}
	ErrClockBack    = errClockBack{}
	ErrExists       = errExists{}
	ErrLimit        = errLimit{}
	ErrNotFound     = errNotFound{}
)

type errInvalidParam struct{}

func (errInvalidParam) Error() string { return "cred: invalid parameter" }

type errClockBack struct{}

func (errClockBack) Error() string { return "cred: clock moved backwards" }

type errExists struct{}

func (errExists) Error() string { return "cred: keyID already exists" }

type errLimit struct{}

func (errLimit) Error() string { return "cred: signing credential limit exceeded" }

type errNotFound struct{}

func (errNotFound) Error() string { return "cred: not found" }

// StatusActive / StatusRetiring / StatusDisabled 暴露给 verify 判定可用性。
const (
	StatusActive   = statusActive
	StatusRetiring = statusRetiring
	StatusDisabled = statusDisabled
)

// Credential 是校验时使用的凭证只读快照。
type Credential struct {
	KeyID        string
	Tenant       string
	Secret       string
	Region       string
	Service      string
	Status       status
	ValidUntil   int64
	RevokeBefore int64
}

type credential struct {
	keyID      string
	tenant     string
	secret     string
	region     string
	service    string
	status     status
	validUntil int64
}

type tenantEntry struct {
	revokeBefore int64
	keyIDs       []string
}

// Store 是并发安全的多租户凭证库。
type Store struct {
	mu      sync.Mutex
	now     int64
	epoch   uint64
	tenants map[string]*tenantEntry
	creds   map[string]*credential
	probes  uint64
}

// NewStore 创建空凭证库。
func NewStore() *Store {
	return &Store{tenants: map[string]*tenantEntry{}, creds: map[string]*credential{}}
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Rotate 登记新凭证并把租户原 active 凭证转入 retiring。
func (s *Store) Rotate(tenant, keyID, secret, region, service string, g, now int64) error {
	if tenant == "" || keyID == "" || secret == "" || region == "" || service == "" ||
		g < 1 || g > maxGrace || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClockBack
	}
	if _, ok := s.creds[keyID]; ok {
		return ErrExists
	}
	ten := s.tenants[tenant]
	if ten == nil {
		ten = &tenantEntry{}
		s.tenants[tenant] = ten
	}
	signable := 0
	for _, id := range ten.keyIDs {
		c := s.creds[id]
		if c.status == statusActive ||
			(c.status == statusRetiring && now < c.validUntil) {
			signable++
		}
	}
	if signable >= maxSigners {
		return ErrLimit
	}
	for _, id := range ten.keyIDs {
		c := s.creds[id]
		if c.status == statusActive {
			c.status = statusRetiring
			c.validUntil = now + g
		}
	}
	s.creds[keyID] = &credential{
		keyID: keyID, tenant: tenant, secret: secret, region: region,
		service: service, status: statusActive,
	}
	ten.keyIDs = append(ten.keyIDs, keyID)
	s.now = now
	s.epoch++
	return nil
}

// Disable 立即停用凭证（已停用为空操作，不耗纪元）。
func (s *Store) Disable(keyID string, now int64) error {
	if keyID == "" || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClockBack
	}
	c, ok := s.creds[keyID]
	if !ok {
		return ErrNotFound
	}
	s.now = now
	if c.status != statusDisabled {
		c.status = statusDisabled
		s.epoch++
	}
	return nil
}

// RevokeBefore 令租户 ts 严格小于 t 的签名失效（t 只增不减）。
func (s *Store) RevokeBefore(tenant string, t, now int64) error {
	if t < 0 || t > maxNow || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClockBack
	}
	ten, ok := s.tenants[tenant]
	if !ok || len(ten.keyIDs) == 0 {
		return ErrNotFound
	}
	s.now = now
	if t > ten.revokeBefore {
		ten.revokeBefore = t
		s.epoch++
	}
	return nil
}

// Epoch 返回当前纪元（仅状态真正改变时递增）。
func (s *Store) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// CredProbes 返回按 keyID 查表的累计次数（测试用）。
func (s *Store) CredProbes() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

// Snapshot 按 keyID 做恰好一次查表，返回校验所需快照；不存在返回 ok=false。
func (s *Store) Snapshot(keyID string) (Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes++
	c, ok := s.creds[keyID]
	if !ok {
		return Credential{}, false
	}
	return Credential{
		KeyID: c.keyID, Tenant: c.tenant, Secret: c.secret, Region: c.region,
		Service: c.service, Status: c.status, ValidUntil: c.validUntil,
		RevokeBefore: s.tenants[c.tenant].revokeBefore,
	}, true
}
