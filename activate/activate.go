package activate

import (
	"sync"

	"ontology/guard"
	"ontology/roster"
)

var (
	ErrInvalid     = roster.ErrInvalid
	ErrClockBack   = roster.ErrClockBack
	ErrUnknown     = roster.ErrUnknown
	ErrLocked      = roster.ErrLocked
	ErrConflict    = roster.ErrConflict
	ErrBatchClosed = roster.ErrBatchClosed
	ErrQuota       = roster.ErrQuota
	ErrDupSn       = roster.ErrDupSn
	ErrState       = roster.ErrState
)

type (
	Sn     = roster.Sn
	Batch  = roster.Batch
	Tenant = roster.Tenant
	Fp     = roster.Fp
)

// Result 是一次成功 Activate 的设备编号与密钥世代。
type Result struct {
	ID  int64
	Gen int64
}

// Service 组合名单、错误尝试守卫与租户名额。
// 锁序固定为 roster.mu -> svc.mu（通过 Hooks 在 roster 临界区内回调），
// 因而全部操作等价于某个串行顺序。
type Service struct {
	mu    sync.Mutex
	rs    *roster.Roster
	gd    *guard.Guard
	quota map[Tenant]int
	used  map[Tenant]int
}

func New(rs *roster.Roster, gd *guard.Guard) *Service {
	return &Service{
		rs:    rs,
		gd:    gd,
		quota: make(map[Tenant]int),
		used:  make(map[Tenant]int),
	}
}

// AddTenant 建立租户与已激活设备名额；重复建立或名额越界报 ErrInvalid。
func (s *Service) AddTenant(tenant Tenant, n int) error {
	if tenant == "" || n < 1 || n > 1_000_000 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.quota[tenant]; ok {
		return ErrInvalid
	}
	s.quota[tenant] = n
	s.used[tenant] = 0
	return nil
}

// RegisterBatch 委托 roster 登记；租户必须已建立。
func (s *Service) RegisterBatch(batch Batch, tenant Tenant, until int64, sns []Sn, now int64) error {
	if tenant == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	_, ok := s.quota[tenant]
	s.mu.Unlock()
	if !ok {
		return ErrInvalid
	}
	return s.rs.RegisterBatch(batch, tenant, until, sns, now)
}

func (s *Service) Activate(sn Sn, fp Fp, now int64) (Result, error) {
	rec, err := s.rs.Gate(sn, fp, now, s)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: rec.ID, Gen: rec.Gen}, nil
}

func (s *Service) Reset(sn Sn, now int64) error {
	_, err := s.rs.Reset(sn, now, s)
	return err
}

func (s *Service) Deactivate(sn Sn, now int64) error {
	_, err := s.rs.Deactivate(sn, now, s)
	return err
}

// ---- roster.Hooks：在 roster 临界区内执行，只获取 svc.mu，不再进入 roster。----

func (s *Service) Locked(sn roster.Sn, now int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gd.Locked(guard.Sn(sn), now)
}

func (s *Service) NoteConflict(sn roster.Sn, now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gd.Note(guard.Sn(sn), now)
}

func (s *Service) AcquireQuota(tenant roster.Tenant) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[tenant] >= s.quota[tenant] {
		return false
	}
	s.used[tenant]++
	return true
}

func (s *Service) ClearGuard(sn roster.Sn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gd.Clear(guard.Sn(sn))
}

func (s *Service) ReleaseQuota(tenant roster.Tenant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[tenant] > 0 {
		s.used[tenant]--
	}
}
