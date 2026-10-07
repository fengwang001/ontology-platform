package ontology

import (
	"sort"
	"sync"
	"time"
)

// linkType 是一个链接类型的定义。
type linkType struct {
	id         string
	sourceType string
	targetType string
	limits     [2]int // 每个方向的基础上限，Unlimited 表示不限
}

// groupKey 标识一个 (链接类型, 方向, 对象) 登记组。
type groupKey struct {
	typeID string
	dir    Direction
	objID  string
}

// group 是一个登记组的运行期状态。
//
// 复杂度不变量：pending 数量通过 pendingList 增量维护，
// 读取待处理数量为 O(1)，与历史基数调整次数无关。
type group struct {
	links        []string // 已登记链接 ID，按 (Seq, ID) 升序，删除后移除
	pendingList  []string // 待处理链接 ID，按标记顺序（先被标记的在前）
	pendingSet   map[string]struct{}
	pinned       map[string]struct{} // 经保留动作固定为有效的链接，不再参与超额选择
	keepOverride int                 // 保留动作提升的有效上限额度
	totalEver    int                 // 历史累计登记数，只增不减
	adjustments  int                 // 历史基数调整次数
}

func newGroup() *group {
	return &group{
		pendingSet: make(map[string]struct{}),
		pinned:     make(map[string]struct{}),
	}
}

// objectRec 是一个已注册对象的记录。
type objectRec struct {
	id      string
	revoked bool
}

// Manager 是链接基数管理器。
// 所有变更操作在同一把互斥锁下串行生效，因此并发操作的结果
// 必然等价于按某个全序串行执行（可线性化）。
type Manager struct {
	mu        sync.Mutex
	types     map[string]*linkType
	links     map[string]*Link
	deleted   map[string]*Link // 已最终删除的链接（保留审计信息）
	groups    map[groupKey]*group
	objects   map[string]*objectRec
	derived   map[string]*DerivedState
	linkDeriv map[string][]string // linkID -> derived IDs
	seq       uint64
	auditSeq  uint64
	audit     []AuditEvent
	opLog     []appliedOp
}

// NewManager 创建一个空的管理器。
func NewManager() *Manager {
	return &Manager{
		types:     make(map[string]*linkType),
		links:     make(map[string]*Link),
		deleted:   make(map[string]*Link),
		groups:    make(map[groupKey]*group),
		objects:   make(map[string]*objectRec),
		derived:   make(map[string]*DerivedState),
		linkDeriv: make(map[string][]string),
	}
}

// DefineLinkType 定义链接类型及两个方向的基础上限（Unlimited 表示不限）。
func (m *Manager) DefineLinkType(id, sourceType, targetType string, sourceLimit, targetLimit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.types[id]; ok {
		return ErrLinkTypeExists
	}
	if sourceLimit < Unlimited || targetLimit < Unlimited || sourceLimit == 0 || targetLimit == 0 {
		return ErrInvalidLimit
	}
	m.types[id] = &linkType{
		id:         id,
		sourceType: sourceType,
		targetType: targetType,
		limits:     [2]int{sourceLimit, targetLimit},
	}
	return nil
}

// RegisterObject 注册一个对象。
func (m *Manager) RegisterObject(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[id]; !ok {
		m.objects[id] = &objectRec{id: id}
	}
}

// RevokeObject 撤销一个对象。此后所有依赖该对象的待处理链接
// 只能转为删除（该判定优先于默认的保留/删除处理路径）。
func (m *Manager) RevokeObject(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[id]; ok {
		obj.revoked = true
	}
}

// IsRevoked 查询对象是否已被撤销。
func (m *Manager) IsRevoked(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[id]
	return ok && obj.revoked
}

// effectiveLimit 返回登记组当前的有效上限（基础上限 + 保留提升额度）。
func (m *Manager) effectiveLimit(lt *linkType, g *group, dir Direction) int {
	base := lt.limits[dir]
	if base == Unlimited {
		return Unlimited
	}
	return base + g.keepOverride
}

// emit 追加一条审计记录。
func (m *Manager) emit(ev AuditEvent) {
	m.auditSeq++
	ev.Seq = m.auditSeq
	ev.Time = time.Now()
	m.audit = append(m.audit, ev)
}

// recordOp 记录一个已串行生效的变更操作，供并发对照测试重放。
func (m *Manager) recordOp(op appliedOp) {
	m.opLog = append(m.opLog, op)
}

// appliedOps 返回已生效操作的全序（测试用）。
func (m *Manager) appliedOps() []appliedOp {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]appliedOp, len(m.opLog))
	copy(out, m.opLog)
	return out
}

// AuditLog 返回全部审计记录（按生效顺序）。
func (m *Manager) AuditLog() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.audit))
	copy(out, m.audit)
	return out
}

// statusOf 计算链接当前状态：任一方向登记组中处于待处理即为 pending。
func (m *Manager) statusOf(l *Link) LinkStatus {
	if _, ok := m.deleted[l.ID]; ok {
		return StatusDeleted
	}
	for _, dir := range []Direction{DirectionOut, DirectionIn} {
		objID := l.Source
		if dir == DirectionIn {
			objID = l.Target
		}
		if g, ok := m.groups[groupKey{l.TypeID, dir, objID}]; ok {
			if _, pend := g.pendingSet[l.ID]; pend {
				return StatusPending
			}
		}
	}
	return StatusActive
}

// GetLink 查询单条链接（含已删除），待处理链接仍然可见。
func (m *Manager) GetLink(id string) (LinkView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[id]; ok {
		return LinkView{Link: *l, Status: m.statusOf(l)}, nil
	}
	if l, ok := m.deleted[id]; ok {
		return LinkView{Link: *l, Status: StatusDeleted}, nil
	}
	return LinkView{}, ErrLinkNotFound
}

// QueryLinks 查询某个 (类型, 方向, 对象) 登记组的全部链接，
// 待处理链接对查询可见并带有明确状态；按 (Seq, ID) 确定性排序返回。
func (m *Manager) QueryLinks(typeID string, dir Direction, objID string) []LinkView {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupKey{typeID, dir, objID}]
	if !ok {
		return nil
	}
	out := make([]LinkView, 0, len(g.links))
	for _, id := range g.links {
		l := m.links[id]
		out = append(out, LinkView{Link: *l, Status: m.statusOf(l)})
	}
	return out
}

// PendingCount 返回某登记组当前处于待处理状态的链接数量。
// 该操作只读取增量维护的计数，开销为 O(1)，
// 与历史上发生过的基数调整次数无关。
func (m *Manager) PendingCount(typeID string, dir Direction, objID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[groupKey{typeID, dir, objID}]; ok {
		return len(g.pendingList)
	}
	return 0
}

// Stats 返回某登记组的统计信息。待处理链接计入 TotalRegistered。
func (m *Manager) Stats(typeID string, dir Direction, objID string) GroupStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	lt, tok := m.types[typeID]
	g, gok := m.groups[groupKey{typeID, dir, objID}]
	if !gok {
		st := GroupStats{EffectiveLimit: Unlimited}
		if tok {
			st.EffectiveLimit = lt.limits[dir]
		}
		return st
	}
	eff := Unlimited
	if tok {
		eff = m.effectiveLimit(lt, g, dir)
	}
	return GroupStats{
		Active:          len(g.links) - len(g.pendingList),
		Pending:         len(g.pendingList),
		TotalRegistered: g.totalEver,
		EffectiveLimit:  eff,
		Adjustments:     g.adjustments,
	}
}

// sortLinkIDs 按 (Seq, ID) 对链接 ID 排序，保证确定性。
func (m *Manager) sortLinkIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.links[ids[i]], m.links[ids[j]]
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.ID < b.ID
	})
}
