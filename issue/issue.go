package issue

import (
	"sync"

	"ontology/bloodstock"
	"ontology/typing"
)

// Role 角色位掩码。
type Role uint8

const (
	RoleCrossmatch Role = 1 << iota // 配血
	RoleIssue                       // 发血
	RoleSupervisor                  // 主管
)

// Manager 红细胞配血与发血管理器。所有公开方法可并发调用，内部以单锁串行化，
// 结果等价于某个串行顺序。
type Manager struct {
	M, H int64

	mu    sync.Mutex
	now   int64
	store *bloodstock.Store
	book  *typing.Book
	roles map[string]Role

	examinedCross     int64
	examinedLandSlack int64
}

func New(M, H int64) *Manager {
	return &Manager{
		M:     M,
		H:     H,
		store: bloodstock.NewStore(M, H),
		book:  typing.NewBook(),
		roles: map[string]Role{},
	}
}

func parseRole(role string) (Role, bool) {
	switch role {
	case "配血":
		return RoleCrossmatch, true
	case "发血":
		return RoleIssue, true
	case "主管":
		return RoleSupervisor, true
	default:
		return 0, false
	}
}

// Grant 授权。非法角色名或空用户静默忽略（无 now、不参与拒绝次序）。
func (m *Manager) Grant(user, role string) {
	r, ok := parseRole(role)
	if !ok || user == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roles[user] |= r
}

func (m *Manager) hasRole(user string, r Role) bool {
	return m.roles[user]&r != 0
}

func (m *Manager) acceptLand(now int64) {
	r := m.store.Land(now)
	m.examinedLandSlack = int64(r.Popped - r.Landed)
	m.store.PruneStale()
	m.now = now
}

// AddBag 添加血袋。
func (m *Manager) AddBag(now int64, bag, abo, rh string, exp int64) error {
	if bag == "" || exp <= now {
		return bloodstock.ErrInvalid
	}
	a, ok1 := bloodstock.ParseABO(abo)
	r2, ok2 := bloodstock.ParseRh(rh)
	if !ok1 || !ok2 {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	if _, exists := m.store.Get(bag); exists {
		return bloodstock.ErrDuplicate
	}
	m.acceptLand(now)
	return m.store.AddBag(bag, a, r2, exp)
}

// Type 记录一次血型鉴定。
func (m *Manager) Type(now int64, user, patient, sample, abo, rh string) error {
	if user == "" || patient == "" || sample == "" {
		return bloodstock.ErrInvalid
	}
	a, ok1 := bloodstock.ParseABO(abo)
	r2, ok2 := bloodstock.ParseRh(rh)
	if !ok1 || !ok2 {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	if m.book.HasSample(sample) {
		return bloodstock.ErrDuplicate
	}
	m.acceptLand(now)
	disputed, err := m.book.Type(patient, sample, a, r2)
	if err != nil {
		return err
	}
	if disputed {
		m.store.ReleasePatient(patient)
	}
	return nil
}

// Resolve 主管裁定血型。
func (m *Manager) Resolve(now int64, supervisor, patient, abo, rh string) error {
	if supervisor == "" || patient == "" {
		return bloodstock.ErrInvalid
	}
	a, ok1 := bloodstock.ParseABO(abo)
	r2, ok2 := bloodstock.ParseRh(rh)
	if !ok1 || !ok2 {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	if m.book.Get(patient).Count == 0 {
		return bloodstock.ErrNotFound
	}
	if !m.hasRole(supervisor, RoleSupervisor) {
		return bloodstock.ErrUnauthorized
	}
	m.acceptLand(now)
	return m.book.Resolve(patient, a, r2)
}

// Crossmatch 预留 n 袋，全有或全无。库存不足为只读判定，被拒绝不落地、不推进时钟。
func (m *Manager) Crossmatch(now int64, tech, patient string, n int) ([]string, error) {
	if patient == "" || tech == "" || n < 1 || n > 20 {
		return nil, bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return nil, bloodstock.ErrClock
	}
	if !m.hasRole(tech, RoleCrossmatch) {
		return nil, bloodstock.ErrUnauthorized
	}
	// 只读干跑：Pick 按落地投影判定，任何结果都不改结构。
	pick, err := m.store.Pick(now, m.book.Groups(patient), n)
	if err != nil {
		return nil, err // 库存不足：零副作用，时钟不推进
	}
	m.acceptLand(now)
	for _, bag := range pick.Bags {
		m.store.Reserve(bag, patient, now)
	}
	m.examinedCross = int64(pick.Examined)
	return append([]string(nil), pick.Bags...), nil
}

// Issue 双人核对发血。
func (m *Manager) Issue(now int64, n1, n2, patient, bag string) error {
	if n1 == "" || n2 == "" || patient == "" || bag == "" || n1 == n2 {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	b, ok := m.store.Get(bag)
	if !ok {
		return bloodstock.ErrNotFound
	}
	if !m.hasRole(n1, RoleIssue) || !m.hasRole(n2, RoleIssue) {
		return bloodstock.ErrUnauthorized
	}
	// 投影状态判定（只读）。
	switch bloodstock.Projected(b, now, m.H) {
	case bloodstock.Reserved:
		// 投影后仍为有效预留才允许发血
	default:
		return bloodstock.ErrState
	}
	if b.Patient != patient {
		return bloodstock.ErrState
	}
	m.acceptLand(now)
	m.store.Issue(bag, now)
	return nil
}

// Return 已发血袋 30 分钟内（含恰等）可退回。
func (m *Manager) Return(now int64, user, bag string) error {
	if user == "" || bag == "" {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	b, ok := m.store.Get(bag)
	if !ok {
		return bloodstock.ErrNotFound
	}
	if !m.hasRole(user, RoleIssue) {
		return bloodstock.ErrUnauthorized
	}
	if b.Status != bloodstock.Issued {
		return bloodstock.ErrState
	}
	if now-b.IssuedAt > 30 {
		return bloodstock.ErrTimeout
	}
	m.acceptLand(now)
	m.store.Return(now, bag)
	return nil
}

// Discard 报废非报废血袋（主管）。
func (m *Manager) Discard(now int64, user, bag string) error {
	if user == "" || bag == "" {
		return bloodstock.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.now {
		return bloodstock.ErrClock
	}
	b, ok := m.store.Get(bag)
	if !ok {
		return bloodstock.ErrNotFound
	}
	if !m.hasRole(user, RoleSupervisor) {
		return bloodstock.ErrUnauthorized
	}
	// 按落地投影判定：投影后已是报废则属「状态不符」，拒绝且零效果。
	if bloodstock.Projected(b, now, m.H) == bloodstock.Discarded {
		return bloodstock.ErrState
	}
	m.acceptLand(now)
	m.store.Discard(bag)
	return nil
}

// ExaminedCross 最近一次成功 Crossmatch 的考察血袋数（含空组探测与效期排除）。
func (m *Manager) ExaminedCross() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.examinedCross
}

// ExaminedLandSlack 最近一次落地「取出事件数 - 实际落地数」，恒 <= 1。
func (m *Manager) ExaminedLandSlack() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.examinedLandSlack
}

// Snapshot 供测试与朴素模拟比对。
type Snapshot struct {
	Now   int64
	Bags  map[string]bloodstock.Bag
	Types map[string]typing.Record
	Roles map[string]Role
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	roles := make(map[string]Role, len(m.roles))
	for k, v := range m.roles {
		roles[k] = v
	}
	return Snapshot{
		Now:   m.now,
		Bags:  m.store.Snapshot(),
		Types: m.book.Snapshot(),
		Roles: roles,
	}
}

// TypeRecord 暴露某患者鉴定记录（供测试与朴素模拟）。
func (m *Manager) TypeRecord(patient string) typing.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.book.Get(patient)
}
