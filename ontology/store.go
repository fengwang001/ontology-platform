package ontology

import "sync"

// 本文件实现已提交状态存储：对象/链接的持久化状态、版本号、
// 历史序列（只记录被接受的调用）、校验审计日志与独立的失败轨迹。
//
// 并发控制采用乐观方案（BOCC）：
//   - 执行开始时在锁内拷贝一份一致快照；
//   - 校验与计划生成全部在快照上进行，不持有任何锁；
//   - 提交时在锁内校验读集版本未变（被读对象未被并发修改/撤销），
//     通过则原子地应用写入计划、递增版本号并追加历史。
//
// 版本号与各类序号只在提交成功的临界区内分配，因此被拒绝的调用
// 不会消耗任何版本号或序号，也不会进入任何历史序列。

// HistoryEntry 是历史序列中的一条记录，只对应被接受的调用。
type HistoryEntry struct {
	Seq        int64
	CallID     string
	ActionType string
	// Version 是对象历史中专用的字段：本次提交后对象的新版本号。
	Version int64
}

// ConditionOutcome 记录单个条件的一次求值结论。
type ConditionOutcome struct {
	ID     string
	Passed bool
}

// StateBasis 记录一次校验所依据的状态（校验依据），供事后核对。
type StateBasis struct {
	// ObjectVersions 是校验期间读取过的对象及其版本号；
	// 版本为 -1 表示校验依据包含“该对象不存在”这一事实。
	ObjectVersions map[ObjectID]int64
	// LinkGen 是校验期间观察到的链接代际号；LinksRead 为 false 时无意义。
	LinksRead bool
	LinkGen   int64
}

// ValidationRecord 记录一次校验（前置或后置）的输入、依据与结论。
type ValidationRecord struct {
	// Seq 是审计日志自身的序号，与对象版本号、历史序号完全独立。
	Seq        int64
	CallID     string
	ActionType string
	Phase      Phase
	// Input 是校验输入：动作参数与目标对象。
	Params  map[string]any
	Targets []ObjectID
	// Basis 是校验依据的状态。
	Basis StateBasis
	// Outcomes 是各条件的求值结论。
	// 前置阶段包含全部条件；后置阶段只包含到决定性条件为止的前缀。
	Outcomes []ConditionOutcome
	// PassedAll 为 true 表示该阶段全部已评估条件通过。
	PassedAll bool
}

// FailureRecord 是独立失败轨迹中的一条记录。
// 失败轨迹与对象状态、版本号、历史序列完全隔离，仅供事后审计。
type FailureRecord struct {
	Seq        int64
	CallID     string
	ActionType string
	Category   RejectCategory
	// ValidationSeqs 指向本次失败相关的校验记录。
	ValidationSeqs []int64
	Detail         string
}

// Store 是已提交状态的存储。
type Store struct {
	mu        sync.Mutex
	objects   map[ObjectID]*Object
	links     map[LinkID]*Link
	linkGen   int64
	commitSeq int64

	// 历史序列：只包含被接受的调用。
	objectHistory map[ObjectID][]HistoryEntry
	actionHistory map[string][]HistoryEntry

	// 审计与失败轨迹：与对象状态隔离。
	validationLog []ValidationRecord
	failureTrail  []FailureRecord
	auditSeq      int64
	failureSeq    int64
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		objects:       make(map[ObjectID]*Object),
		links:         make(map[LinkID]*Link),
		objectHistory: make(map[ObjectID][]HistoryEntry),
		actionHistory: make(map[string][]HistoryEntry),
	}
}

// Seed 直接写入一个对象的初始已提交状态（版本 0），供初始化/测试使用。
func (s *Store) Seed(obj Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	props := make(map[string]any, len(obj.Props))
	for k, v := range obj.Props {
		props[k] = v
	}
	o := obj
	o.Props = props
	o.Version = 0
	s.objects[o.ID] = &o
}

// snapshot 是已提交状态在某一时刻的一致拷贝。
type snapshot struct {
	objects map[ObjectID]Object
	links   map[LinkID]Link
	linkGen int64
}

// takeSnapshot 在锁内拷贝全部已提交状态。
//
// 取舍说明：全量拷贝换取实现简单与严格的一致性；生产实现应替换为
// MVCC 版本链，接口（一致快照 + 读集校验）保持不变。
func (s *Store) takeSnapshot() *snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := &snapshot{
		objects: make(map[ObjectID]Object, len(s.objects)),
		links:   make(map[LinkID]Link, len(s.links)),
		linkGen: s.linkGen,
	}
	for id, o := range s.objects {
		props := make(map[string]any, len(o.Props))
		for k, v := range o.Props {
			props[k] = v
		}
		snap.objects[id] = Object{ID: o.ID, Type: o.Type, Props: props, Version: o.Version}
	}
	for id, l := range s.links {
		snap.links[id] = *l
	}
	return snap
}

// commitResult 是提交尝试的结果。
type commitResult struct {
	ok        bool
	commitSeq int64
	// conflict 描述冲突对象（用于错误详情）。
	conflict ObjectID
}

// commit 在锁内校验读集并原子应用写入计划。
//
// reads 是执行期间读取过的对象及其快照版本（-1 表示读取时对象不存在）；
// linksRead/linkGen 是链接读集。任何一项与当前已提交状态不一致，
// 说明执行期间发生了并发修改/撤销，提交被拒绝且不产生任何状态变化。
func (s *Store) commit(
	reads map[ObjectID]int64,
	linksRead bool,
	linkGen int64,
	plan *WritePlan,
	callID string,
	actionType string,
) commitResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ver := range reads {
		cur, exists := s.objects[id]
		if ver < 0 {
			if exists {
				return commitResult{conflict: id}
			}
			continue
		}
		if !exists || cur.Version != ver {
			return commitResult{conflict: id}
		}
	}
	if linksRead && s.linkGen != linkGen {
		return commitResult{conflict: "<links>"}
	}
	// 应用对象写入：最终计划（map），同一对象的中间写入已被覆盖。
	for id, w := range plan.objects {
		switch w.kind {
		case writeCreate:
			props := make(map[string]any, len(w.props))
			for k, v := range w.props {
				props[k] = v
			}
			s.objects[id] = &Object{ID: id, Type: w.objType, Props: props, Version: 1}
		case writeUpdate:
			cur := s.objects[id]
			props := make(map[string]any, len(w.props))
			for k, v := range w.props {
				props[k] = v
			}
			cur.Props = props
			cur.Version++
		case writeDelete:
			delete(s.objects, id)
		}
	}
	// 应用链接写入。
	if len(plan.links) > 0 {
		s.linkGen++
		for id, w := range plan.links {
			switch w.kind {
			case writeCreate:
				s.links[id] = &Link{ID: id, Type: w.linkType, From: w.from, To: w.to, Version: 1}
			case writeDelete:
				delete(s.links, id)
			}
		}
	}
	s.commitSeq++
	seq := s.commitSeq
	// 追加历史序列：仅被接受的调用。
	for id := range plan.objects {
		var version int64
		if cur, ok := s.objects[id]; ok {
			version = cur.Version
		}
		entry := HistoryEntry{
			Seq:        int64(len(s.objectHistory[id]) + 1),
			CallID:     callID,
			ActionType: actionType,
			Version:    version,
		}
		s.objectHistory[id] = append(s.objectHistory[id], entry)
	}
	s.actionHistory[actionType] = append(s.actionHistory[actionType], HistoryEntry{
		Seq:    int64(len(s.actionHistory[actionType]) + 1),
		CallID: callID,
	})
	return commitResult{ok: true, commitSeq: seq}
}

// appendValidation 追加一条校验记录（审计日志），返回其序号。
// 审计序号独立于对象版本号与历史序号。
func (s *Store) appendValidation(rec ValidationRecord) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditSeq++
	rec.Seq = s.auditSeq
	s.validationLog = append(s.validationLog, rec)
	return rec.Seq
}

// appendFailure 追加一条失败轨迹记录，返回其序号。
func (s *Store) appendFailure(rec FailureRecord) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureSeq++
	rec.Seq = s.failureSeq
	s.failureTrail = append(s.failureTrail, rec)
	return rec.Seq
}

// ---- 只读访问器（供测试与审计查询；均在锁内拷贝返回） ----

// GetObject 返回对象的当前已提交状态。
func (s *Store) GetObject(id ObjectID) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok {
		return Object{}, false
	}
	props := make(map[string]any, len(o.Props))
	for k, v := range o.Props {
		props[k] = v
	}
	return Object{ID: o.ID, Type: o.Type, Props: props, Version: o.Version}, true
}

// ObjectIDs 返回全部现存对象 ID。
func (s *Store) ObjectIDs() []ObjectID {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]ObjectID, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	return ids
}

// ObjectHistory 返回对象的已接受调用历史。
func (s *Store) ObjectHistory(id ObjectID) []HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HistoryEntry(nil), s.objectHistory[id]...)
}

// ActionHistory 返回动作类型的已接受调用历史。
func (s *Store) ActionHistory(actionType string) []HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HistoryEntry(nil), s.actionHistory[actionType]...)
}

// ValidationLog 返回全部校验记录。
func (s *Store) ValidationLog() []ValidationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ValidationRecord(nil), s.validationLog...)
}

// FailureTrail 返回独立失败轨迹。
func (s *Store) FailureTrail() []FailureRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FailureRecord(nil), s.failureTrail...)
}

// CommitSeq 返回当前全局提交序号。
func (s *Store) CommitSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitSeq
}
