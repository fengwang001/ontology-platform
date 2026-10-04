package notify

import (
	"errors"
	"sync"

	"ontology/alert"
	"ontology/labrule"
)

var (
	ErrInvalid      = errors.New("notify: invalid argument")
	ErrClockBack    = errors.New("notify: clock moved backwards")
	ErrNotFound     = errors.New("notify: test, patient or event not found")
	ErrUnauthorized = errors.New("notify: unauthorized")
	ErrState        = errors.New("notify: invalid event state")
	ErrDuplicate    = errors.New("notify: duplicate test code")
)

// Role 病区角色。
type Role int

const (
	Nurse Role = iota
	Doctor
)

// ResultOutcome 是 Result 上报的结果。
type ResultOutcome struct {
	Normal   bool    // 非危急结果（复查正常不影响任何事件）
	EventID  int64   // 危急结果所属事件
	Created  bool    // 是否新建事件
	Upgraded bool    // 是否本次升级
	Sev      int     // 事件当前严重度
	Rep      int64   // 事件当前代表值
	Deadline int64   // 事件当前时限
	LandNow  []int64 // 本操作开头新落地逾期的事件号
}

// NotifyOutcome 是 Notify 的结果。
type NotifyOutcome struct {
	EventID int64
	LandNow []int64
}

// ReadBackOutcome 是 ReadBack 的结果。Mismatch=true 表示回读值不符（操作仍被接受）。
type ReadBackOutcome struct {
	EventID  int64
	Mismatch bool
	State    alert.State
	LandNow  []int64
}

// ActOutcome 是 Act 的结果。
type ActOutcome struct {
	EventID int64
	Late    bool
	LandNow []int64
}

type grant struct {
	ward string
	role Role
}

// Manager 闭环管理器。一把互斥锁串行化全部操作，并发调用等价于某个串行顺序。
type Manager struct {
	mu      sync.Mutex
	rules   *labrule.Rules
	board   *alert.Board
	t       [4]int64 // T[1..3]
	maxNow  int64
	ward    map[string]string             // patient -> 当前病区
	perm    map[string]map[grant]struct{} // user -> 资格集合
	overdue []int64                       // 逾期清单（事件号不重复）
}

// New 创建管理器，骨架占位。
func New(t1, t2, t3 int) (*Manager, error) {
	t := [4]int64{0, int64(t1), int64(t2), int64(t3)}
	for i := 1; i <= 3; i++ {
		if t[i] < 1 || t[i] > 10_000 {
			return nil, ErrInvalid
		}
	}
	return &Manager{
		rules: labrule.NewRules(),
		board: alert.NewBoard(),
		t:     t,
		ward:  make(map[string]string),
		perm:  make(map[string]map[grant]struct{}),
	}, nil
}

// AddTest 登记检验项目。纯配置操作，不落地逾期、不推进时钟。
func (m *Manager) AddTest(code string, low, high, step int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.rules.AddTest(code, low, high, step); err != nil {
		if errors.Is(err, labrule.ErrInvalid) {
			return ErrInvalid
		}
		if errors.Is(err, labrule.ErrDuplicateTest) {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

// SetWard 设置患者当前病区。纯配置操作。
func (m *Manager) SetWard(patient, ward string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if patient == "" || ward == "" {
		return ErrInvalid
	}
	m.ward[patient] = ward
	return nil
}

// Grant 授予用户在某病区的护士或医生资格。纯配置操作。
func (m *Manager) Grant(user, ward string, role Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == "" || ward == "" || (role != Nurse && role != Doctor) {
		return ErrInvalid
	}
	g, ok := m.perm[user]
	if !ok {
		g = make(map[grant]struct{})
		m.perm[user] = g
	}
	g[grant{ward: ward, role: role}] = struct{}{}
	return nil
}

func validTime(now int64) bool { return now >= 0 && now <= 1_000_000_000 }

// begin 校验时钟并推进，随后落地当前时刻所有新逾期事件。
func (m *Manager) begin(now int64) []int64 {
	m.maxNow = now
	landed := m.board.LandOverdue(now)
	ids := make([]int64, 0, len(landed))
	for _, e := range landed {
		m.overdue = append(m.overdue, e.ID)
		ids = append(ids, e.ID)
	}
	return ids
}

// Result 上报一条结果。非危急结果不影响任何事件（复查正常不能代替闭环）。
func (m *Manager) Result(now int64, patient, code string, v int64) (ResultOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validTime(now) || patient == "" || code == "" || v < -1_000_000_000 || v > 1_000_000_000 {
		return ResultOutcome{}, ErrInvalid
	}
	if now < m.maxNow {
		return ResultOutcome{}, ErrClockBack
	}
	if !m.rules.Has(code) || !m.hasWard(patient) {
		return ResultOutcome{}, ErrNotFound
	}
	land := m.begin(now)
	sev, crit := m.rules.Severity(code, v)
	if !crit {
		return ResultOutcome{Normal: true, LandNow: land}, nil
	}
	e, created, upgraded := m.board.Ingest(now, patient, code, v, sev, func(s int) int64 { return m.t[s] })
	return ResultOutcome{
		EventID:  e.ID,
		Created:  created,
		Upgraded: upgraded,
		Sev:      e.Sev,
		Rep:      e.Rep,
		Deadline: e.Deadline,
		LandNow:  land,
	}, nil
}

// Notify 对事件发起通知。
func (m *Manager) Notify(now int64, event int64, tech, receiver string) (NotifyOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validTime(now) || event < 1 || tech == "" || receiver == "" {
		return NotifyOutcome{}, ErrInvalid
	}
	if now < m.maxNow {
		return NotifyOutcome{}, ErrClockBack
	}
	e := m.board.Get(event)
	if e == nil || !m.hasWard(e.Patient) {
		return NotifyOutcome{}, ErrNotFound
	}
	land := m.begin(now)
	if !m.hasRole(receiver, m.ward[e.Patient], Nurse) && !m.hasRole(receiver, m.ward[e.Patient], Doctor) {
		return NotifyOutcome{}, ErrUnauthorized
	}
	if e.State != alert.StateNotify {
		return NotifyOutcome{}, ErrState
	}
	e.State = alert.StateReadBack
	e.Tech = tech
	e.Receiver = receiver
	return NotifyOutcome{EventID: event, LandNow: land}, nil
}

// ReadBack 由接收人回读代表值。
func (m *Manager) ReadBack(now int64, event int64, receiver string, v int64) (ReadBackOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validTime(now) || event < 1 || receiver == "" || v < -1_000_000_000 || v > 1_000_000_000 {
		return ReadBackOutcome{}, ErrInvalid
	}
	if now < m.maxNow {
		return ReadBackOutcome{}, ErrClockBack
	}
	e := m.board.Get(event)
	if e == nil || !m.hasWard(e.Patient) {
		return ReadBackOutcome{}, ErrNotFound
	}
	land := m.begin(now)
	if e.State != alert.StateReadBack {
		return ReadBackOutcome{}, ErrState
	}
	// 仅在“待回读”下，接收人非本次通知接收人才报无资格；
	// 非待回读状态（如升级/两次不符退回待通知）按题面样例报状态不符。
	if receiver != e.Receiver {
		return ReadBackOutcome{}, ErrUnauthorized
	}
	out := ReadBackOutcome{EventID: event, LandNow: land}
	if v != e.Rep {
		e.Mismatch++
		out.Mismatch = true
		if e.Mismatch >= 2 {
			e.State = alert.StateNotify
			e.Mismatch = 0
			e.Receiver = ""
			e.Tech = ""
		}
		out.State = e.State
		return out, nil
	}
	e.State = alert.StateAct
	out.State = e.State
	return out, nil
}

// Act 由医生完成处置并闭环事件；Late 为事件是否已逾期（粘滞）。
func (m *Manager) Act(now int64, event int64, doctor string) (ActOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validTime(now) || event < 1 || doctor == "" {
		return ActOutcome{}, ErrInvalid
	}
	if now < m.maxNow {
		return ActOutcome{}, ErrClockBack
	}
	e := m.board.Get(event)
	if e == nil || !m.hasWard(e.Patient) {
		return ActOutcome{}, ErrNotFound
	}
	land := m.begin(now)
	if !m.hasRole(doctor, m.ward[e.Patient], Doctor) {
		return ActOutcome{}, ErrUnauthorized
	}
	if e.State != alert.StateAct {
		return ActOutcome{}, ErrState
	}
	late := e.Late
	m.board.Close(e)
	return ActOutcome{EventID: event, Late: late, LandNow: land}, nil
}

// Overdue 返回逾期清单快照（按落地先后，事件号不重复）。
func (m *Manager) Overdue() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int64, len(m.overdue))
	copy(out, m.overdue)
	return out
}

func (m *Manager) hasWard(patient string) bool {
	_, ok := m.ward[patient]
	return ok
}

func (m *Manager) hasRole(user, ward string, role Role) bool {
	g, ok := m.perm[user]
	if !ok {
		return false
	}
	_, ok = g[grant{ward: ward, role: role}]
	return ok
}
