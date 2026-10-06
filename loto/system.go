package loto

import (
	"container/heap"
	"sort"
	"sync"
)

// Permit 工作票。
type Permit struct {
	ID        int
	Applicant string
	Devices   []string // 排序去重后的设备集合
	Type      WorkType
	Start     int64 // 计划时段起点（含）
	End       int64 // 计划时段终点（不含）
	State     State

	approvers  map[string]bool            // 已批准人集合
	workers    map[string]bool            // 登记在票上的作业人员
	points     []string                   // 涉及隔离点全集（各设备依赖隔离点的并集，排序）
	pointSet   map[string]bool            // points 的集合形式
	locks      map[string]map[string]bool // 隔离点 -> 持锁人集合（仅本票）
	onSite     map[string]bool            // 当前在场人员
	mustRelock map[string]bool            // 被强制摘除锁、再次进入前须重新上锁的人
	trialSaved map[string]map[string]bool // 试运行期间暂时解除的锁记录：隔离点 -> 原持锁人集合
}

// Points 返回该票涉及的隔离点全集（所有设备依赖隔离点的并集）。
func (p *Permit) Points() []string { return append([]string(nil), p.points...) }

// expiryItem 逾期扫描堆元素。
type expiryItem struct {
	end      int64
	permitID int
}

type expiryHeap []expiryItem

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].end < h[j].end }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiryItem)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// System 能量隔离上锁挂牌工作票系统。
// 所有公开方法均可并发调用，内部以单一互斥锁串行化，
// 结果等价于按获得锁的某个串行顺序执行。
type System struct {
	mu sync.Mutex

	cfg          *Config
	pointDevices map[string][]string // 隔离点 -> 依赖它的设备
	points       map[string]bool     // 全部隔离点
	people       map[string]map[Role]bool
	permits      map[int]*Permit
	nextPermitID int
	clock        int64 // 最近被接受操作的时刻
	clockSet     bool

	// 以下索引/计数器只随“占用态票与现存锁”变化，与历史票总数无关。
	active       map[int]*Permit         // 占用态票
	pointLocks   map[string]int          // 隔离点 -> 锁总数（所有票）
	deviceLocks  map[string]int          // 设备 -> 其依赖隔离点上的锁总数
	deviceActive map[string]map[int]bool // 设备 -> 涉及它的占用态票
	expiry       expiryHeap              // 占用态票按终点时刻的最小堆

	audit []AuditEntry
}

// NewSystem 依据配置创建系统。
func NewSystem(cfg *Config) (*System, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cp := &Config{DevicePoints: map[string][]string{}}
	for dev, pts := range cfg.DevicePoints {
		sorted := append([]string(nil), pts...)
		sort.Strings(sorted)
		cp.DevicePoints[dev] = sorted
	}
	s := &System{
		cfg:          cp,
		pointDevices: cp.devicesOfPoint(),
		points:       map[string]bool{},
		people:       map[string]map[Role]bool{},
		permits:      map[int]*Permit{},
		nextPermitID: 1,
		active:       map[int]*Permit{},
		pointLocks:   map[string]int{},
		deviceLocks:  map[string]int{},
		deviceActive: map[string]map[int]bool{},
	}
	for pt := range s.pointDevices {
		s.points[pt] = true
	}
	return s, nil
}

// AddPerson 登记人员及其角色（可多次调用追加角色）。属配置操作，不带时刻。
func (s *System) AddPerson(id string, roles ...Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return newErr("AddPerson", ErrInvalidParam, "人员编号不能为空")
	}
	if len(roles) == 0 {
		return newErr("AddPerson", ErrInvalidParam, "人员 %s 至少需要一个角色", id)
	}
	if s.people[id] == nil {
		s.people[id] = map[Role]bool{}
	}
	for _, r := range roles {
		s.people[id][r] = true
	}
	return nil
}

// Now 返回系统时钟（最近被接受操作的时刻）。
func (s *System) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// Audit 返回审计日志快照（仅含被接受的操作）。
func (s *System) Audit() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEntry(nil), s.audit...)
}

// PermitState 查询票状态（只读）。
func (s *System) PermitState(id int) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.permits[id]
	if !ok {
		return 0, newErr("PermitState", ErrNotFound, "工作票 %d 不存在", id)
	}
	return p.stateAt(s.clock), nil
}

// ---- 内部辅助 ----

// checkTime 校验时刻：非负且不早于上一个被接受操作的时刻。
func (s *System) checkTime(op string, t int64) error {
	if t < 0 {
		return newErr(op, ErrInvalidParam, "时刻不能为负: %d", t)
	}
	if s.clockSet && t < s.clock {
		return newErr(op, ErrTimeRegression, "时刻 %d 早于上一个被接受操作的时刻 %d", t, s.clock)
	}
	return nil
}

// accept 接受一个操作：推进时钟、执行逾期扫描、写审计。
func (s *System) accept(t int64, op string, permitID int, actor, detail string) {
	s.clock = t
	s.clockSet = true
	s.sweepOverdue(t)
	s.audit = append(s.audit, AuditEntry{Time: t, Op: op, PermitID: permitID, Actor: actor, Detail: detail})
}

// sweepOverdue 将计划终点已到且仍未完成的占用态票转为逾期。
// 只在操作被接受后调用，因此不会违反“被拒绝的操作不改变状态”。
func (s *System) sweepOverdue(t int64) {
	for len(s.expiry) > 0 && s.expiry[0].end <= t {
		it := heap.Pop(&s.expiry).(expiryItem)
		p, ok := s.active[it.permitID]
		if !ok || p.End != it.end {
			continue // 已完成或已不在占用态的惰性堆项
		}
		s.setState(p, StateOverdue)
	}
}

// setState 切换票状态并维护占用态索引与送电阻止计数。
func (s *System) setState(p *Permit, st State) {
	old := p.State
	if old == st {
		return
	}
	p.State = st
	if st.Occupied() && !old.Occupied() {
		s.active[p.ID] = p
		heap.Push(&s.expiry, expiryItem{end: p.End, permitID: p.ID})
		for _, d := range p.Devices {
			if s.deviceActive[d] == nil {
				s.deviceActive[d] = map[int]bool{}
			}
			s.deviceActive[d][p.ID] = true
		}
	}
	if !st.Occupied() && old.Occupied() {
		delete(s.active, p.ID)
		for _, d := range p.Devices {
			delete(s.deviceActive[d], p.ID)
		}
	}
	// 送电阻止判定在 EnergizableAt 中基于 deviceActive（仅占用态票）即时计算。
}

// addLock 在隔离点上挂锁并维护计数（不校验，调用方负责）。
func (s *System) addLock(p *Permit, person, point string) {
	if p.locks[point] == nil {
		p.locks[point] = map[string]bool{}
	}
	p.locks[point][person] = true
	s.pointLocks[point]++
	for _, d := range s.pointDevices[point] {
		s.deviceLocks[d]++
	}
}

// removeLock 摘除隔离点上某人的锁并维护计数（不校验，调用方负责）。
func (s *System) removeLock(p *Permit, person, point string) {
	delete(p.locks[point], person)
	if len(p.locks[point]) == 0 {
		delete(p.locks, point)
	}
	s.pointLocks[point]--
	for _, d := range s.pointDevices[point] {
		s.deviceLocks[d]--
	}
}

// locksComplete 报告是否全员完成全部隔离点上锁。
func (p *Permit) locksComplete() bool {
	for w := range p.workers {
		for _, pt := range p.points {
			if !p.locks[pt][w] {
				return false
			}
		}
	}
	return true
}

// refreshLockState 在上锁/登记人员变化后，于 已生效<->已上锁 之间切换。
func (s *System) refreshLockState(p *Permit) {
	if p.State != StateEffective && p.State != StateLocked {
		return
	}
	if p.locksComplete() {
		s.setState(p, StateLocked)
	} else {
		s.setState(p, StateEffective)
	}
}

// stateAt 返回票在时刻 t 的虚拟状态（占用态且终点已到视为逾期），不修改任何状态。
func (p *Permit) stateAt(t int64) State {
	if p.State.Occupied() && p.State != StateOverdue && t >= p.End {
		return StateOverdue
	}
	return p.State
}
