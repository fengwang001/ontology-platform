// Package naive 是朴素全锁定对照模型：语义与 ontology.Store 完全一致，
// 但实现上采用单把全局互斥锁、保留全部历史足迹并在判定时扫描
// 全局提交日志，以此作为随机并发等价性测试的独立参照实现。
package naive

import (
	"fmt"
	"sync"

	"ontology/ontology"
)

// versionRecord 保留某一版本的完整历史信息（朴素模型不做 O(1) 优化）。
type versionRecord struct {
	version   uint64
	props     map[string]string
	writeSet  map[string]struct{}
	readSet   map[string]struct{}
	commitSeq uint64
}

type object struct {
	id       string
	typeName string
	deleted  bool
	history  []versionRecord
}

// commitEntry 是全局提交日志的一条记录。
type commitEntry struct {
	seq      uint64
	objectID string
	typeName string
	writeSet map[string]struct{}
}

// Model 是朴素对照模型。
type Model struct {
	mu      sync.Mutex
	types   map[string]ontology.ObjectType
	objects map[string]*object
	commits []commitEntry
	seq     uint64
}

// New 创建空模型。
func New() *Model {
	return &Model{
		types:   make(map[string]ontology.ObjectType),
		objects: make(map[string]*object),
	}
}

// RegisterObjectType 注册对象类型。
func (m *Model) RegisterObjectType(t ontology.ObjectType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.types[t.Name] = t
}

// Create 创建实例并提交初始版本。
func (m *Model) Create(typeName, id string, initial map[string]string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.types[typeName]; !ok {
		return 0, fmt.Errorf("naive: unknown object type %q", typeName)
	}
	if _, ok := m.objects[id]; ok {
		return 0, fmt.Errorf("naive: object %q already exists", id)
	}
	obj := &object{id: id, typeName: typeName}
	m.objects[id] = obj
	return m.commitLocked(obj, 0, initial)
}

// Delete 逻辑删除实例。
func (m *Model) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[id]; ok {
		obj.deleted = true
	}
}

// Version 返回实例当前版本号。
func (m *Model) Version(id string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj := m.objects[id]
	if obj == nil || len(obj.history) == 0 {
		return 0
	}
	return obj.history[len(obj.history)-1].version
}

// Props 返回实例指定版本的属性副本。
func (m *Model) Props(id string, version uint64) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj := m.objects[id]
	if obj == nil {
		return nil
	}
	for _, vr := range obj.history {
		if vr.version == version {
			out := make(map[string]string, len(vr.props))
			for k, v := range vr.props {
				out[k] = v
			}
			return out
		}
	}
	return nil
}

// Write 在全局锁内串行提交一次写入，语义与 ontology.Store.Write 一致。
func (m *Model) Write(req ontology.WriteRequest) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[req.ObjectID]
	if !ok {
		return 0, ontology.ErrNotFound
	}
	return m.commitLocked(obj, req.Baseline, req.Set)
}

func (m *Model) readSetsLocked(typeName string, writeSet map[string]struct{}) (map[string]struct{}, map[ontology.RemoteRead]struct{}) {
	local := make(map[string]struct{})
	remote := make(map[ontology.RemoteRead]struct{})
	t, ok := m.types[typeName]
	if !ok {
		return local, remote
	}
	for _, h := range t.Hooks {
		for _, p := range h.ReadProps {
			local[p] = struct{}{}
		}
		for _, rr := range h.RemoteReads {
			remote[ontology.RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}] = struct{}{}
		}
		if h.DeclareReads != nil {
			dl, dr := h.DeclareReads(writeSet)
			for _, p := range dl {
				local[p] = struct{}{}
			}
			for _, rr := range dr {
				remote[ontology.RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}] = struct{}{}
			}
		}
	}
	return local, remote
}

func (m *Model) commitLocked(obj *object, baseline uint64, set map[string]string) (uint64, error) {
	var current uint64
	if len(obj.history) > 0 {
		current = obj.history[len(obj.history)-1].version
	}

	// 1. 已删除检查优先。
	if obj.deleted {
		return 0, &ontology.WriteError{Kind: ontology.ErrKindDeleted, ObjectID: obj.id,
			Baseline: baseline, Current: current}
	}

	// 2. 基线落后检查。
	if baseline+1 < current {
		return 0, &ontology.WriteError{Kind: ontology.ErrKindStaleBaseline, ObjectID: obj.id,
			Baseline: baseline, Current: current}
	}

	writeSet := make(map[string]struct{}, len(set))
	for k := range set {
		writeSet[k] = struct{}{}
	}
	readLocal, readRemote := m.readSetsLocked(obj.typeName, writeSet)

	// 3a. 本地属性级冲突：扫描完整历史，取最近一次提交的足迹重算。
	if baseline+1 == current {
		last := obj.history[len(obj.history)-1]
		for p := range writeSet {
			if _, ok := last.writeSet[p]; ok {
				return 0, propConflict(obj, baseline, current, p)
			}
			if _, ok := last.readSet[p]; ok {
				return 0, propConflict(obj, baseline, current, p)
			}
		}
		for p := range readLocal {
			if _, ok := last.writeSet[p]; ok {
				return 0, propConflict(obj, baseline, current, p)
			}
			if _, ok := last.readSet[p]; ok {
				return 0, propConflict(obj, baseline, current, p)
			}
		}
	}

	// 3b. 跨实例冲突：扫描全局提交日志，收集本实例当前版本创建之后
	//     发生的、且被本实例钩子声明订阅的远程写入。
	var sinceSeq uint64
	if len(obj.history) > 0 {
		sinceSeq = obj.history[len(obj.history)-1].commitSeq
	}
	pending := make(map[ontology.RemoteRead]struct{})
	for _, e := range m.commits {
		if e.seq <= sinceSeq || e.objectID == obj.id {
			continue
		}
		for p := range e.writeSet {
			key := ontology.RemoteRead{TargetType: e.typeName, Prop: p}
			pending[key] = struct{}{}
		}
	}
	for rr := range readRemote {
		key := ontology.RemoteRead{TargetType: rr.TargetType, Prop: rr.Prop}
		if _, ok := pending[key]; ok {
			return 0, &ontology.WriteError{Kind: ontology.ErrKindPropertyConflict,
				ObjectID: obj.id, Baseline: baseline, Current: current,
				RemoteProps: []ontology.RemoteRead{key}}
		}
	}

	// 4. 校验钩子。
	merged := make(map[string]string)
	if len(obj.history) > 0 {
		for k, v := range obj.history[len(obj.history)-1].props {
			merged[k] = v
		}
	}
	for k, v := range set {
		merged[k] = v
	}
	if t, ok := m.types[obj.typeName]; ok {
		for _, h := range t.Hooks {
			if h.Validate == nil {
				continue
			}
			if err := h.Validate(merged); err != nil {
				return 0, &ontology.WriteError{Kind: ontology.ErrKindValidation,
					ObjectID: obj.id, Baseline: baseline, Current: current,
					Detail: fmt.Sprintf("hook %q: %v", h.Name, err)}
			}
		}
	}

	// 5. 提交。
	m.seq++
	obj.history = append(obj.history, versionRecord{
		version:   current + 1,
		props:     merged,
		writeSet:  writeSet,
		readSet:   readLocal,
		commitSeq: m.seq,
	})
	m.commits = append(m.commits, commitEntry{
		seq:      m.seq,
		objectID: obj.id,
		typeName: obj.typeName,
		writeSet: writeSet,
	})
	return current + 1, nil
}

func propConflict(obj *object, baseline, current uint64, prop string) error {
	return &ontology.WriteError{Kind: ontology.ErrKindPropertyConflict, ObjectID: obj.id,
		Baseline: baseline, Current: current, Props: []string{prop}}
}
