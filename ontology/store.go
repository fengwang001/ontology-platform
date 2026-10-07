package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// Store 双时态链接历史存储与审计入口。
//
// 线性化保证：所有公开方法在同一把互斥锁下执行，每个操作在锁内获得
// 全局单调序号 seq；因此任意并发操作集合的最终可观察结果，等价于按
// seq 给出的全局顺序串行执行。审计为纯只读操作，不触碰事实与快照。
type Store struct {
	mu      sync.Mutex
	seq     int64
	objects map[ObjectTypeID]int64 // 对象类型 -> 创建时的记录时刻
	links   map[LinkTypeID]*linkTypeState

	auditLog []AuditRecord

	// 可独立验证的回放开销探针（见 Stats）。
	lastReplaySteps int64
}

// linkTypeState 单个链接类型的全部历史状态。
type linkTypeState struct {
	def      LinkTypeDef
	facts    []Fact              // 追加式事实日志，RecordedAt 单调递增
	snaps    []snapshot          // 每 snapshotStride 条事实物化一次
	versions []ConstraintVersion // 按 EffectiveFrom 升序
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		objects: make(map[ObjectTypeID]int64),
		links:   make(map[LinkTypeID]*linkTypeState),
	}
}

// Now 返回当前记录时刻（全局序号的最新值）。
// 客户端可据此构造审计请求的记录时刻区间。
func (s *Store) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// RegisterObjectType 注册对象类型，返回其创建的记录时刻。
func (s *Store) RegisterObjectType(id ObjectTypeID) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at, ok := s.objects[id]; ok {
		return at
	}
	s.seq++
	s.objects[id] = s.seq
	return s.seq
}

// RegisterLinkType 注册链接类型，并建立初始基数约束版本
// （Version 1，双侧不限，自记录时刻 0 起生效）。
func (s *Store) RegisterLinkType(def LinkTypeDef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[def.ID]; ok {
		return fmt.Errorf("link type %q already registered", def.ID)
	}
	if _, ok := s.objects[def.LeftType]; !ok {
		return fmt.Errorf("left object type %q not registered", def.LeftType)
	}
	if _, ok := s.objects[def.RightType]; !ok {
		return fmt.Errorf("right object type %q not registered", def.RightType)
	}
	s.links[def.ID] = &linkTypeState{
		def: def,
		versions: []ConstraintVersion{{
			Version:       1,
			EffectiveFrom: 0,
			Left:          unconstrained,
			Right:         unconstrained,
		}},
	}
	return nil
}

// AdjustCardinality 调整基数约束，产生自当前记录时刻起生效的新版本，
// 返回新版本号。历史记录时刻的审计仍使用当时生效的旧版本。
func (s *Store) AdjustCardinality(lt LinkTypeID, left, right Cardinality) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.links[lt]
	if !ok {
		return 0, fmt.Errorf("link type %q not registered", lt)
	}
	s.seq++
	v := ConstraintVersion{
		Version:       len(st.versions) + 1,
		EffectiveFrom: s.seq,
		Left:          left,
		Right:         right,
	}
	st.versions = append(st.versions, v)
	return v.Version, nil
}

// FactInput 原始事实录入参数。Source 仅供内部对称性缺损判定，
// 常规写入请使用 CreateLink / RevokeLink（等价于 EndpointBoth）。
type FactInput struct {
	Kind      FactKind
	Left      ObjectID
	Right     ObjectID
	ValidFrom int64
	ValidTo   int64
	Source    Endpoint
}

// CreateLink 以系统级规范记录创建链接（双端同时生效）。
func (s *Store) CreateLink(lt LinkTypeID, left, right ObjectID, validFrom int64) error {
	return s.RecordFact(lt, FactInput{
		Kind: FactCreate, Left: left, Right: right,
		ValidFrom: validFrom, Source: EndpointBoth,
	})
}

// RevokeLink 撤销链接，关闭其有效时间区间。
func (s *Store) RevokeLink(lt LinkTypeID, left, right ObjectID, validTo int64) error {
	return s.RecordFact(lt, FactInput{
		Kind: FactRevoke, Left: left, Right: right,
		ValidTo: validTo, Source: EndpointBoth,
	})
}

// RecordFact 追加一条双时态事实。事实在锁内获得全局序号与记录时刻，
// 规范化后写入只追加日志，并按需物化快照。
func (s *Store) RecordFact(lt LinkTypeID, in FactInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.links[lt]
	if !ok {
		return fmt.Errorf("link type %q not registered", lt)
	}
	if st.def.Symmetric && in.Left == in.Right {
		return errors.New("symmetric link type rejects self-pair")
	}
	s.seq++
	p := canonicalPair(st.def, in.Left, in.Right)
	st.facts = append(st.facts, Fact{
		Seq:        s.seq,
		RecordedAt: s.seq,
		Kind:       in.Kind,
		Left:       p.L,
		Right:      p.R,
		ValidFrom:  in.ValidFrom,
		ValidTo:    in.ValidTo,
		source:     in.Source,
	})
	if len(st.facts)%snapshotStride == 0 {
		from := len(st.facts) - snapshotStride
		var prev *snapshot
		if n := len(st.snaps); n > 0 {
			prev = &st.snaps[n-1]
		}
		st.snaps = append(st.snaps, materialize(prev, st.facts, from, len(st.facts), st.def))
	}
	return nil
}

// Stats 存储运行探针，用于独立验证回放开销与历史规模无关。
type Stats struct {
	Facts           int   `json:"facts"`
	Snapshots       int   `json:"snapshots"`
	Versions        int   `json:"versions"`
	LastReplaySteps int64 `json:"lastReplaySteps"`
}

// Stats 返回指定链接类型的运行探针数据。
func (s *Store) Stats(lt LinkTypeID) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.links[lt]
	if st == nil {
		return Stats{}
	}
	return Stats{
		Facts:           len(st.facts),
		Snapshots:       len(st.snaps),
		Versions:        len(st.versions),
		LastReplaySteps: s.lastReplaySteps,
	}
}

// DecisionLog 返回全部审计判定记录（追加式，含输入、所依据的
// 约束版本与结论），供事后核查。
func (s *Store) DecisionLog() []AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditRecord, len(s.auditLog))
	copy(out, s.auditLog)
	return out
}
