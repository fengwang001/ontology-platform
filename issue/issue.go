package issue

import (
	"errors"
	"fmt"
	"sync"

	"ontology/bloodstock"
	"ontology/typing"
)

type Role int

const (
	RoleTech Role = iota // 配血
	RoleIssue
	RoleSupervisor
)

var (
	ErrInvalid    = errors.New("invalid argument")
	ErrClock      = errors.New("clock moved backwards")
	ErrNotFound   = errors.New("patient or bag not found")
	ErrSampleDup  = errors.New("duplicate sample")
	ErrBagDup     = errors.New("duplicate bag")
	ErrForbidden  = errors.New("user not qualified")
	ErrState      = errors.New("status mismatch")
	ErrStock      = errors.New("insufficient stock")
	ErrReturnLate = errors.New("return window exceeded")
)

type Manager struct {
	mu sync.Mutex

	m, h   int64
	maxNow int64

	store *bloodstock.Store
	types *typing.Registry
	roles map[string]map[Role]bool

	logf func(format string, args ...any)
}

func New(M, H int64) *Manager {
	return &Manager{
		m:     M,
		h:     H,
		store: bloodstock.NewStore(M),
		types: typing.NewRegistry(),
		roles: map[string]map[Role]bool{},
	}
}

// SetLogger 注入判定日志（打印输入、输出与判定依据）。
func (m *Manager) SetLogger(f func(format string, args ...any)) { m.logf = f }

func (m *Manager) log(format string, args ...any) {
	if m.logf != nil {
		m.logf(format, args...)
	}
}

func (m *Manager) Grant(user string, role Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grantLocked(user, role)
}

func (m *Manager) grantLocked(user string, role Role) {
	if m.roles[user] == nil {
		m.roles[user] = map[Role]bool{}
	}
	m.roles[user][role] = true
}

func (m *Manager) hasRole(user string, role Role) bool {
	return m.roles[user] != nil && m.roles[user][role]
}

// precheck 执行：参数 -> 时钟 -> 存在 -> 资格（不落地、不推进时钟）。
// 通过后调用方负责 Land 并做状态级判定。
func (m *Manager) precheck(now int64) error {
	if now < m.maxNow {
		m.log("拒绝 now=%d 时钟回退 maxNow=%d", now, m.maxNow)
		return ErrClock
	}
	return nil
}

func (m *Manager) land(now int64) {
	applied := m.store.Land(now, func(bag string) {
		m.log("落地 now=%d 血袋 %s 效期报废", now, bag)
	})
	m.log("落地 now=%d applied=%d popped=%d", now, applied, m.store.LandPopped)
}

func (m *Manager) acceptClock(now int64) {
	if now > m.maxNow {
		m.maxNow = now
	}
}

func validABO(a bloodstock.ABO) bool { return a >= bloodstock.A && a <= bloodstock.O }
func validRH(r bloodstock.Rh) bool   { return r == bloodstock.Positive || r == bloodstock.Negative }

func (m *Manager) AddBag(now int64, bag string, abo bloodstock.ABO, rh bloodstock.Rh, exp int64) error {
	if bag == "" || !validABO(abo) || !validRH(rh) || exp <= now || now < 0 || now > 1e9 {
		m.log("AddBag 拒绝 参数非法 now=%d bag=%q exp=%d", now, bag, exp)
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	if _, exists := m.store.Get(bag); exists {
		m.log("AddBag 拒绝 血袋重复 %s", bag)
		return ErrBagDup
	}
	m.land(now)
	m.store.Add(bag, abo, rh, exp)
	m.acceptClock(now)
	m.log("AddBag 接受 now=%d bag=%s abo=%d rh=%d exp=%d", now, bag, abo, rh, exp)
	return nil
}

func (m *Manager) Type(now int64, user, patientName, sample string, abo bloodstock.ABO, rh bloodstock.Rh) error {
	if patientName == "" || sample == "" || !validABO(abo) || !validRH(rh) || now < 0 || now > 1e9 {
		m.log("Type 拒绝 参数非法")
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	if m.types.KnownSample(sample) {
		m.log("Type 拒绝 标本重复 sample=%s", sample)
		return ErrSampleDup
	}
	m.land(now)
	kind, contradicted := m.types.Type(now, patientName, sample, abo, rh)
	m.types.PutSample(sample)
	if contradicted {
		n := m.store.ReleasePatient(patientName)
		m.log("Type now=%d 患者 %s 结果不一致->存疑，释放预留 %d 袋", now, patientName, n)
	}
	m.acceptClock(now)
	m.log("Type 接受 now=%d patient=%s sample=%s -> kind=%d", now, patientName, sample, kind)
	return nil
}

func (m *Manager) Resolve(now int64, supervisor, patientName string, abo bloodstock.ABO, rh bloodstock.Rh) error {
	if patientName == "" || supervisor == "" || !validABO(abo) || !validRH(rh) || now < 0 || now > 1e9 {
		m.log("Resolve 拒绝 参数非法")
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	if !m.hasRole(supervisor, RoleSupervisor) {
		m.log("Resolve 拒绝 无资格 %s", supervisor)
		return ErrForbidden
	}
	m.land(now)
	m.types.Resolve(patientName, abo, rh)
	m.acceptClock(now)
	m.log("Resolve 接受 now=%d patient=%s abo=%d rh=%d", now, patientName, abo, rh)
	return nil
}

func (m *Manager) Crossmatch(now int64, tech, patientName string, n int) (bags []string, examined int, err error) {
	if n < 1 || n > 20 || tech == "" || patientName == "" || now < 0 || now > 1e9 {
		m.log("Crossmatch 拒绝 参数非法 n=%d", n)
		return nil, 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.precheck(now); err != nil {
		return nil, 0, err
	}
	if !m.hasRole(tech, RoleTech) {
		m.log("Crossmatch 拒绝 无资格 %s", tech)
		return nil, 0, ErrForbidden
	}
	m.land(now)
	order := m.types.Order(patientName)
	picked, examined, ok := m.store.Reserve(now, m.h, order, patientName, n)
	if !ok {
		m.acceptClock(now)
		m.log("Crossmatch 库存不足 now=%d patient=%s n=%d examined=%d", now, patientName, n, examined)
		return nil, examined, ErrStock
	}
	m.acceptClock(now)
	m.log("Crossmatch 接受 now=%d patient=%s n=%d picked=%v examined=%d order=%v", now, patientName, n, picked, examined, order)
	return picked, examined, nil
}

func (m *Manager) Issue(now int64, n1, n2, patientName, bag string) error {
	if n1 == "" || n2 == "" || n1 == n2 || patientName == "" || bag == "" || now < 0 || now > 1e9 {
		m.log("Issue 拒绝 参数非法")
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	if _, ok := m.store.Get(bag); !ok {
		m.log("Issue 拒绝 血袋不存在 %s", bag)
		return ErrNotFound
	}
	if !m.hasRole(n1, RoleIssue) || !m.hasRole(n2, RoleIssue) {
		m.log("Issue 拒绝 无资格 %s/%s", n1, n2)
		return ErrForbidden
	}
	m.land(now)
	if !m.store.MarkIssued(now, patientName, bag) {
		m.acceptClock(now)
		m.log("Issue 拒绝 状态不符 bag=%s patient=%s", bag, patientName)
		return ErrState
	}
	m.acceptClock(now)
	m.log("Issue 接受 now=%d bag=%s patient=%s by=%s,%s", now, bag, patientName, n1, n2)
	return nil
}

func (m *Manager) Return(now int64, user, bag string) error {
	if user == "" || bag == "" || now < 0 || now > 1e9 {
		m.log("Return 拒绝 参数非法")
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	b, ok := m.store.Get(bag)
	if !ok {
		m.log("Return 拒绝 血袋不存在 %s", bag)
		return ErrNotFound
	}
	if !m.hasRole(user, RoleIssue) {
		m.log("Return 拒绝 无资格 %s", user)
		return ErrForbidden
	}
	m.land(now)
	if b.Status != bloodstock.Issued {
		m.acceptClock(now)
		m.log("Return 拒绝 状态不符 bag=%s status=%d", bag, b.Status)
		return ErrState
	}
	if now-b.IssuedAt > 30 {
		m.acceptClock(now)
		m.log("Return 拒绝 超时 bag=%s issuedAt=%d now=%d", bag, b.IssuedAt, now)
		return ErrReturnLate
	}
	existed, expiredNow := m.store.Return(now, bag)
	if !existed {
		m.acceptClock(now)
		return ErrState
	}
	m.acceptClock(now)
	m.log("Return 接受 now=%d bag=%s expiredNow=%v", now, bag, expiredNow)
	return nil
}

func (m *Manager) Discard(now int64, user, bag string) error {
	if user == "" || bag == "" || now < 0 || now > 1e9 {
		m.log("Discard 拒绝 参数非法")
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.precheck(now); err != nil {
		return err
	}
	if _, ok := m.store.Get(bag); !ok {
		m.log("Discard 拒绝 血袋不存在 %s", bag)
		return ErrNotFound
	}
	m.land(now)
	if !m.store.Discard(bag) {
		m.acceptClock(now)
		m.log("Discard 拒绝 状态不符（已报废） bag=%s", bag)
		return ErrState
	}
	m.acceptClock(now)
	m.log("Discard 接受 now=%d by=%s bag=%s", now, user, bag)
	return nil
}

// 以下为测试/校验导出的只读接口。

func (m *Manager) BagStatus(bag string) (bloodstock.Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.store.Get(bag)
	if !ok {
		return 0, false
	}
	return b.Status, true
}

func (m *Manager) BagPatient(bag string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.store.Get(bag)
	if !ok {
		return ""
	}
	return b.Patient
}

func (m *Manager) PatientKind(patientName string) typing.Kind {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, _ := m.types.State(patientName)
	return k
}

func (m *Manager) Examined() int     { return m.store.TotalExamined }
func (m *Manager) EventsPopped() int { return m.store.TotalPopped }
func (m *Manager) EventsApplied() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.TotalApplied
}

func (m *Manager) String() string {
	return fmt.Sprintf("Manager(M=%d,H=%d,maxNow=%d)", m.m, m.h, m.maxNow)
}
