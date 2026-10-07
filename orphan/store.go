package orphan

import (
	"fmt"
	"sync"
)

// objectState 是单个对象的增量索引：只收集与该对象相关的事件，
// 使判定开销与网络累计事件总量无关。
type objectState struct {
	created   bool // 是否已出现 EvObjectCreated
	objType   ObjectTypeID
	createdAt Time
	events    []Event // 追加顺序
}

type linkMeta struct {
	linkType  LinkTypeID
	from, to  ObjectID
	createdAt Time
}

// Store 是并发安全的事件存储与规则版本账本。
// 所有公开操作在单一互斥/读写锁下原子完成，因此任意并发操作的
// 可观察结果等价于按某个全局顺序串行执行（可线性化）。
type Store struct {
	mu       sync.RWMutex
	auditMu  sync.Mutex
	opSeq    uint64
	eventSeq uint64

	events    []Event
	byID      map[EventID]Event
	ledger    []RuleVersion
	objects   map[ObjectID]*objectState
	links     map[LinkID]linkMeta
	linkTypes map[LinkTypeID]Time
	pending   map[LinkID][]Event // 链接尚未创建时暂存的撤销事件

	audit []AuditRecord
}

// NewStore 创建存储并写入初始规则版本 v1。
func NewStore(spec RuleSpec, at Time) *Store {
	s := &Store{
		byID:      make(map[EventID]Event),
		objects:   make(map[ObjectID]*objectState),
		links:     make(map[LinkID]linkMeta),
		linkTypes: make(map[LinkTypeID]Time),
		pending:   make(map[LinkID][]Event),
	}
	s.ledger = append(s.ledger, RuleVersion{
		Version:       1,
		Spec:          spec,
		EffectiveFrom: at,
	})
	return s
}

// Append 原子地追加一批事件并更新增量索引。
// 追加本身不做引用/顺序校验；四类错误在重建与判定时按优先级报告。
func (s *Store) Append(events ...Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		if e.ID == "" {
			return fmt.Errorf("orphan: event ID must not be empty")
		}
		if _, dup := s.byID[e.ID]; dup {
			return fmt.Errorf("orphan: duplicate event ID %q", e.ID)
		}
	}
	for _, e := range events {
		s.eventSeq++
		e.Seq = s.eventSeq
		s.events = append(s.events, e)
		s.byID[e.ID] = e
		s.indexLocked(e)
	}
	s.opSeq++
	return nil
}

// indexLocked 把单条事件登记进增量索引（调用方须持写锁）。
func (s *Store) indexLocked(e Event) {
	switch e.Kind {
	case EvLinkTypeCreated:
		if t, ok := s.linkTypes[e.LinkType]; !ok || e.Time < t {
			s.linkTypes[e.LinkType] = e.Time
		}
	case EvObjectCreated:
		st, ok := s.objects[e.Object]
		if !ok {
			st = &objectState{created: true, objType: e.ObjectType, createdAt: e.Time}
			s.objects[e.Object] = st
		} else if !st.created || e.Time < st.createdAt {
			st.created = true
			st.objType = e.ObjectType
			st.createdAt = e.Time
		}
		st.events = append(st.events, e)
	case EvPropertySet, EvObjectMarkedOrphan:
		if st, ok := s.objects[e.Object]; ok {
			st.events = append(st.events, e)
		} else {
			// 对象尚未创建：仍建立索引槽位，引用合法性在判定时统一检查。
			st = &objectState{}
			s.objects[e.Object] = st
			st.events = append(st.events, e)
		}
	case EvLinkCreated:
		if m, ok := s.links[e.Link]; !ok || e.Time < m.createdAt {
			s.links[e.Link] = linkMeta{linkType: e.LinkType, from: e.From, to: e.To, createdAt: e.Time}
		}
		s.appendToObject(e.From, e)
		if e.To != e.From {
			s.appendToObject(e.To, e)
		}
		if pend, ok := s.pending[e.Link]; ok {
			for _, p := range pend {
				s.appendToObject(e.From, p)
				if e.To != e.From {
					s.appendToObject(e.To, p)
				}
			}
			delete(s.pending, e.Link)
		}
	case EvLinkRevoked:
		if m, ok := s.links[e.Link]; ok {
			s.appendToObject(m.from, e)
			if m.to != m.from {
				s.appendToObject(m.to, e)
			}
		} else {
			s.pending[e.Link] = append(s.pending[e.Link], e)
		}
	}
}

func (s *Store) appendToObject(id ObjectID, e Event) {
	st, ok := s.objects[id]
	if !ok {
		st = &objectState{}
		s.objects[id] = st
	}
	st.events = append(st.events, e)
}

// AdjustRule 追加一个新的规则版本。retroactive=true 表示追溯适用于
// at 之前的全部历史并作废先前版本对历史的治理权。
func (s *Store) AdjustRule(spec RuleSpec, retroactive bool, at Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.ledger[len(s.ledger)-1]
	if at <= last.EffectiveFrom {
		return 0, fmt.Errorf("orphan: rule effective time %d must be after %d", at, last.EffectiveFrom)
	}
	s.opSeq++
	v := RuleVersion{
		Version:       len(s.ledger) + 1,
		Spec:          spec,
		Retroactive:   retroactive,
		EffectiveFrom: at,
		CreateSeq:     s.opSeq,
	}
	s.ledger = append(s.ledger, v)
	return v.Version, nil
}

// RuleLedger 返回规则账本快照。
func (s *Store) RuleLedger() []RuleVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RuleVersion, len(s.ledger))
	copy(out, s.ledger)
	return out
}

// AuditLog 返回判定审计日志快照。
func (s *Store) AuditLog() []AuditRecord {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	out := make([]AuditRecord, len(s.audit))
	copy(out, s.audit)
	return out
}

// Events 返回事件流快照（含存储分配的 Seq）。
func (s *Store) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *Store) recordAudit(rec AuditRecord) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	s.audit = append(s.audit, rec)
}
