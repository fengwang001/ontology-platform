package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound 表示目标实例不存在。它不属于三类冲突错误。
var ErrNotFound = errors.New("ontology: object not found")

// snapshot 是某一版本的不可变快照。
type snapshot struct {
	version uint64
	props   map[string]string
	bytes   []byte // 规范化序列化结果，缓存以保证字节级不变
}

// instance 是单个对象实例的全部可变状态。
// version/current/snapshots/lastFootprint/deleted 由 mu 保护；
// pendingRemote 由 Store.propMu 保护（避免跨实例传播时的锁序死锁）。
type instance struct {
	mu        sync.Mutex
	id        string
	typeName  string
	version   uint64
	deleted   bool
	current   map[string]string
	snapshots map[uint64]*snapshot
	// lastFootprint 是最近一次已提交写入的（写集合 ∪ 相关读集合）
	// 之本实例属性部分。冲突判定只需查阅这一个足迹，
	// 因此开销与历史版本总数无关。
	lastFootprint map[string]struct{}
	// pendingRemote 是自最近一次成功提交以来、经钩子声明订阅的
	// 跨实例属性变化集合；成功提交后清空。被拒绝的写入不得改动它。
	pendingRemote map[RemoteRead]struct{}
}

// Store 是对象实例存储。
type Store struct {
	mu      sync.Mutex
	types   map[string]*ObjectType
	links   []LinkType
	objects map[string]*instance

	// propMu 保护 subs 与所有实例的 pendingRemote。
	// 锁序：instance.mu 可以先于 propMu 持有，反之不允许。
	propMu sync.Mutex
	// subs 是反向订阅索引：(目标类型, 属性) -> 订阅者实例集合。
	// 订阅关系唯一地由校验钩子声明的 RemoteReads 决定。
	// 实例只被逻辑删除、从不从 objects 中移除，故可安全持有指针。
	subs map[RemoteRead]map[string]*instance

	log *DecisionLog
}

// NewStore 创建一个空存储。
func NewStore() *Store {
	return &Store{
		types:   make(map[string]*ObjectType),
		objects: make(map[string]*instance),
		subs:    make(map[RemoteRead]map[string]*instance),
		log:     newDecisionLog(),
	}
}

// RegisterObjectType 注册对象类型。重复注册同名类型会覆盖。
func (s *Store) RegisterObjectType(t ObjectType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := t
	s.types[t.Name] = &cp
}

// RegisterLinkType 注册链接类型（仅为钩子声明提供来源语境）。
func (s *Store) RegisterLinkType(l LinkType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links = append(s.links, l)
}

// Create 创建实例并提交初始版本 1，初始属性即首个写集合。
func (s *Store) Create(typeName, id string, initial map[string]string) (WriteResult, error) {
	s.mu.Lock()
	if _, ok := s.types[typeName]; !ok {
		s.mu.Unlock()
		return WriteResult{}, fmt.Errorf("ontology: unknown object type %q", typeName)
	}
	if _, exists := s.objects[id]; exists {
		s.mu.Unlock()
		return WriteResult{}, fmt.Errorf("ontology: object %q already exists", id)
	}
	inst := &instance{
		id:            id,
		typeName:      typeName,
		current:       make(map[string]string),
		snapshots:     make(map[uint64]*snapshot),
		lastFootprint: make(map[string]struct{}),
		pendingRemote: make(map[RemoteRead]struct{}),
	}
	s.objects[id] = inst
	t := s.types[typeName]
	s.mu.Unlock()

	// 建立钩子声明的跨实例订阅。
	s.propMu.Lock()
	for _, h := range t.Hooks {
		for _, rr := range h.RemoteReads {
			key := RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}
			if s.subs[key] == nil {
				s.subs[key] = make(map[string]*instance)
			}
			s.subs[key][id] = inst
		}
	}
	s.propMu.Unlock()

	return s.commit(inst, WriteRequest{ObjectID: id, Baseline: 0, Set: initial})
}

// Delete 逻辑删除实例。删除后任何写入都以 ErrKindDeleted 拒绝。
func (s *Store) Delete(id string) error {
	inst, err := s.lookup(id)
	if err != nil {
		return err
	}
	inst.mu.Lock()
	inst.deleted = true
	inst.mu.Unlock()
	return nil
}

// Write 提交一次写入。冲突判定在提交时刻、于实例锁内完成。
func (s *Store) Write(req WriteRequest) (WriteResult, error) {
	inst, err := s.lookup(req.ObjectID)
	if err != nil {
		return WriteResult{}, err
	}
	return s.commit(inst, req)
}

func (s *Store) lookup(id string) (*instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.objects[id]
	if !ok {
		return nil, ErrNotFound
	}
	return inst, nil
}

// readSets 汇总对象类型上所有校验钩子为本次写入声明的读取集合。
func (s *Store) readSets(typeName string, writeSet map[string]struct{}) (local map[string]struct{}, remote map[RemoteRead]struct{}) {
	s.mu.Lock()
	t := s.types[typeName]
	s.mu.Unlock()
	local = make(map[string]struct{})
	remote = make(map[RemoteRead]struct{})
	if t == nil {
		return local, remote
	}
	for _, h := range t.Hooks {
		for _, p := range h.ReadProps {
			local[p] = struct{}{}
		}
		for _, rr := range h.RemoteReads {
			remote[RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}] = struct{}{}
		}
		if h.DeclareReads != nil {
			dl, dr := h.DeclareReads(writeSet)
			for _, p := range dl {
				local[p] = struct{}{}
			}
			for _, rr := range dr {
				remote[RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}] = struct{}{}
			}
		}
	}
	return local, remote
}

// commit 是写入的提交路径。全部判定在实例锁内基于提交时刻状态完成。
func (s *Store) commit(inst *instance, req WriteRequest) (WriteResult, error) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	rec := DecisionRecord{
		ObjectID:       inst.id,
		Baseline:       req.Baseline,
		CurrentVersion: inst.version,
		HistoryLen:     len(inst.snapshots),
		WriteValues:    copyProps(req.Set),
	}
	writeSet := make(map[string]struct{}, len(req.Set))
	for k := range req.Set {
		writeSet[k] = struct{}{}
	}
	readLocal, readRemote := s.readSets(inst.typeName, writeSet)
	rec.WriteSet = sortedKeys(writeSet)
	rec.ReadSet = sortedKeys(readLocal)
	rec.RemoteReadSet = sortedRemoteReads(readRemote)

	// 1. 已删除检查优先于属性级冲突检查。
	if inst.deleted {
		rec.Verdict = VerdictDeleted
		rec.Reason = "object is logically deleted"
		s.log.append(rec)
		return WriteResult{}, &WriteError{Kind: ErrKindDeleted, ObjectID: inst.id,
			Baseline: req.Baseline, Current: inst.version}
	}

	// 2. 基线落后检查：仅支持与最新版本或其直接父版本合并，
	//    更早的基线需要查阅多份历史足迹，超出 O(1) 判定预算。
	if req.Baseline+1 < inst.version {
		rec.Verdict = VerdictStaleBaseline
		rec.Reason = fmt.Sprintf("baseline %d is more than one version behind current %d",
			req.Baseline, inst.version)
		s.log.append(rec)
		return WriteResult{}, &WriteError{Kind: ErrKindStaleBaseline, ObjectID: inst.id,
			Baseline: req.Baseline, Current: inst.version}
	}

	// 3. 属性级冲突判定：写集合 ∪ 相关读集合 的交集。
	rec.FootprintsConsulted = 1
	rec.LastFootprint = sortedKeys(inst.lastFootprint)

	if req.Baseline+1 == inst.version {
		// 基线是当前版本的直接父版本：与最近一次已提交写入做交集判定。
		var hits []string
		union := make(map[string]struct{}, len(writeSet)+len(readLocal))
		for p := range writeSet {
			union[p] = struct{}{}
		}
		for p := range readLocal {
			union[p] = struct{}{}
		}
		for p := range union {
			if _, ok := inst.lastFootprint[p]; ok {
				hits = append(hits, p)
			}
		}
		if len(hits) > 0 {
			sort.Strings(hits)
			rec.Verdict = VerdictPropertyConflict
			rec.Reason = "local footprint intersection: " + strings.Join(hits, ",")
			s.log.append(rec)
			return WriteResult{}, &WriteError{Kind: ErrKindPropertyConflict, ObjectID: inst.id,
				Baseline: req.Baseline, Current: inst.version, Props: hits}
		}
	}

	// 从此处到提交完成持有 propMu：远程失效的检查、消费、传播与
	// 判定日志的追加必须在同一临界区内完成，保证日志给出的全局
	// 顺序是这些判定的一个合法串行解释（可串行化证据）。
	// 注意：Validate 回调在该临界区内执行，其实现不得重入 Store。
	s.propMu.Lock()
	defer s.propMu.Unlock()

	rec.PendingRemote = sortedRemoteReads(inst.pendingRemote)

	// 跨实例：相关读集合与挂起的远程失效集合求交。
	var remoteHits []RemoteRead
	for rr := range readRemote {
		key := RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}
		if _, ok := inst.pendingRemote[key]; ok {
			remoteHits = append(remoteHits, key)
		}
	}
	if len(remoteHits) > 0 {
		rec.Verdict = VerdictPropertyConflict
		rec.Reason = fmt.Sprintf("remote read-set intersection: %v", remoteHits)
		s.log.append(rec)
		return WriteResult{}, &WriteError{Kind: ErrKindPropertyConflict, ObjectID: inst.id,
			Baseline: req.Baseline, Current: inst.version, RemoteProps: remoteHits}
	}

	// 4. 校验钩子对合并后的预期状态执行校验。
	merged := make(map[string]string, len(inst.current)+len(req.Set))
	for k, v := range inst.current {
		merged[k] = v
	}
	for k, v := range req.Set {
		merged[k] = v
	}
	s.mu.Lock()
	t := s.types[inst.typeName]
	s.mu.Unlock()
	if t != nil {
		for _, h := range t.Hooks {
			if h.Validate == nil {
				continue
			}
			if err := h.Validate(merged); err != nil {
				rec.Verdict = VerdictValidation
				rec.Reason = fmt.Sprintf("hook %q: %v", h.Name, err)
				s.log.append(rec)
				return WriteResult{}, &WriteError{Kind: ErrKindValidation, ObjectID: inst.id,
					Baseline: req.Baseline, Current: inst.version, Detail: rec.Reason}
			}
		}
	}

	// 5. 提交：版本号严格单调递增（计数器，不依赖时钟取值）。
	inst.version++
	inst.current = merged
	snap := &snapshot{version: inst.version, props: copyProps(merged)}
	snap.bytes = canonicalBytes(merged)
	inst.snapshots[inst.version] = snap

	// 更新最近一次提交的足迹（写集合 ∪ 相关读集合的本实例部分）。
	inst.lastFootprint = make(map[string]struct{}, len(writeSet)+len(readLocal))
	for p := range writeSet {
		inst.lastFootprint[p] = struct{}{}
	}
	for p := range readLocal {
		inst.lastFootprint[p] = struct{}{}
	}

	// 本次提交已"知悉"此前的远程失效，清空挂起集合。
	inst.pendingRemote = make(map[RemoteRead]struct{})

	rec.Verdict = VerdictCommitted
	rec.NewVersion = inst.version
	rec.Reason = "committed"
	id := s.log.append(rec)

	// 6. 跨实例传播：本次写集合中每个属性，凡被其他实例的钩子
	//    声明订阅，即记入那些实例的挂起远程失效集合。
	for p := range writeSet {
		key := RemoteRead{TargetType: inst.typeName, Prop: p}
		for subID, sub := range s.subs[key] {
			if subID == inst.id {
				continue
			}
			sub.pendingRemote[key] = struct{}{}
		}
	}

	return WriteResult{ObjectID: inst.id, Version: inst.version, DecisionID: id}, nil
}

// ReadAt 读取指定版本的快照，返回规范化字节与属性副本。
// 快照不可变：无论之后发生多少次写入，结果字节级不变。
func (s *Store) ReadAt(id string, version uint64) ([]byte, map[string]string, error) {
	inst, err := s.lookup(id)
	if err != nil {
		return nil, nil, err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	snap, ok := inst.snapshots[version]
	if !ok {
		return nil, nil, fmt.Errorf("ontology: object %s has no version %d", id, version)
	}
	out := make([]byte, len(snap.bytes))
	copy(out, snap.bytes)
	return out, copyProps(snap.props), nil
}

// Version 返回实例当前最新已提交版本号。
func (s *Store) Version(id string) (uint64, error) {
	inst, err := s.lookup(id)
	if err != nil {
		return 0, err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.version, nil
}

// Decisions 返回完整判定日志，供重放核验。
func (s *Store) Decisions() []DecisionRecord { return s.log.Records() }

func copyProps(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// canonicalBytes 生成属性集合的规范化序列化形式（键排序）。
func canonicalBytes(m map[string]string) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return []byte(b.String())
}
