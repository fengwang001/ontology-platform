package ontology

import (
	"fmt"
	"sort"
)

// IndexPhase 刻画一个已声明索引的对外可用状态。
type IndexPhase string

const (
	// PhaseAbsent 索引从未重建过：查询语义为“索引不存在”。
	PhaseAbsent IndexPhase = "absent"
	// PhaseRebuilding 重建进行中：尚未产出完整审计记录，查询语义为“不可用”。
	PhaseRebuilding IndexPhase = "rebuilding"
	// PhaseActive 已有完整审计记录封存并对外提供查询。
	PhaseActive IndexPhase = "active"
	// PhaseFailed 上次重建中途失败：部分条目已被整体丢弃，查询语义仍为“不可用”。
	PhaseFailed IndexPhase = "failed"
)

// indexSpec 描述对象类型上声明的一个按值查找索引。
type indexSpec struct {
	objectType string
	name       string
	property   string
}

// indexState 是一个索引的全部运行时状态，始终在 Store 锁保护下迁移。
type indexState struct {
	spec  indexSpec
	phase IndexPhase
	audit *AuditRecord // 封存审计及其后的 RangeDelta 追加（仅 active 非空）

	// 重建进行中的临时工作集；失败时整体丢弃，绝不对外可见。
	building       bool
	baselineEndLSN int64
	completionLSN  int64
	workEntries    map[string]EntryProof // objectID -> 当前生效条目

	// 复核曾发现且尚未修复的不一致（active 下查询仍可进行，但须声明）。
	flaggedMismatch bool
	flagReason      string

	// 测试用故障注入。
	failAfterBaseline bool
	failBeforeSeal    bool
}

// QueryResult 是索引查询结果，携带不一致声明。
type QueryResult struct {
	ObjectIDs             []string
	InconsistencyDeclared bool
	InconsistencyReason   string
	AuditBaselineEndLSN   int64
	AuditCompletionLSN    int64
}

// IndexManager 管理某对象存储之上所有已声明索引的声明、重建与查询。
type IndexManager struct {
	store  *Store
	log    *DecisionLog
	specs  map[string]indexSpec
	states map[string]*indexState

	pendingFailBaseline bool
	pendingFailSet      bool
}

func specKey(objectType, indexName string) string { return objectType + "\x00" + indexName }

func NewIndexManager(store *Store, log *DecisionLog) *IndexManager {
	return &IndexManager{
		store:  store,
		log:    log,
		specs:  make(map[string]indexSpec),
		states: make(map[string]*indexState),
	}
}

// Store 返回管理器绑定的对象存储。
func (m *IndexManager) Store() *Store { return m.store }

func (m *IndexManager) stateLocked(objectType, indexName string) (*indexState, indexSpec, bool) {
	spec, ok := m.specs[specKey(objectType, indexName)]
	if !ok {
		return nil, indexSpec{}, false
	}
	return m.states[specKey(objectType, indexName)], spec, true
}

// Declare 在对象类型上声明一个针对单一属性的按值索引。重复声明幂等。
func (m *IndexManager) Declare(objectType, indexName, property string) {
	m.store.Lock()
	defer m.store.Unlock()
	key := specKey(objectType, indexName)
	m.specs[key] = indexSpec{objectType: objectType, name: indexName, property: property}
	if _, ok := m.states[key]; !ok {
		m.states[key] = &indexState{
			spec:  m.specs[key],
			phase: PhaseAbsent,
		}
	}
}

// IsDeclared 报告索引是否在对象类型上声明。
func (m *IndexManager) IsDeclared(objectType, indexName string) bool {
	m.store.Lock()
	defer m.store.Unlock()
	_, ok := m.specs[specKey(objectType, indexName)]
	return ok
}

// statesForTypeLocked 返回某类型上全部已声明索引状态（调用方持锁）。
func (m *IndexManager) statesForTypeLocked(objectType string) []*indexState {
	out := make([]*indexState, 0)
	for key, st := range m.states {
		if m.specs[key].objectType == objectType {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].spec.name < out[j].spec.name })
	return out
}

// Write 是对象写入的推荐入口：它在同一个全局串行点上先落实对象状态、
// 再把写入对相关索引的影响按写入顺序追加进存活审计，保证索引应用顺序
// 与写入对对象本身生效的顺序一致，不颠倒。
func (m *IndexManager) Write(objectType, objectID string, props map[string]PropertyValue) int64 {
	m.store.Lock()
	lsn := m.store.PutLocked(objectType, objectID, props)
	ver := int64(0)
	if obj := m.store.objects[objectID]; obj != nil {
		ver = obj.version
	}
	for _, st := range m.statesForTypeLocked(objectType) {
		m.applyWriteLocked(st, objectID, props, lsn, ver)
	}
	m.store.Unlock()
	m.log.Write(DecisionRecord{
		Kind:     KindWrite,
		Input:    map[string]interface{}{"objectType": objectType, "objectID": objectID, "props": props},
		Output:   map[string]interface{}{"lsn": lsn, "objectVersion": ver},
		Basis:    "single global serialization point: object state commit and index append share one LSN",
		Decision: "committed",
	})
	return lsn
}

// applyWriteLocked 在锁内把一次写入对单个索引的影响落实到位：
//   - active：以 RangeDelta 语义覆盖存活审计条目；
//   - rebuilding：该写入 LSN 必 > fence，记入增量工作集；
//   - absent/failed：忽略（既无可用索引也无在途重建）。
func (m *IndexManager) applyWriteLocked(st *indexState, objectID string,
	props map[string]PropertyValue, lsn, ver int64) {
	value, touches := props[st.spec.property]
	if !touches {
		return
	}
	switch {
	case st.phase == PhaseActive:
		m.upsertLiveEntryLocked(st, objectID, value, lsn, ver)
		st.audit.CompletionLSN = lsn
		st.audit.Digest = st.audit.ComputeDigest()
	case st.building:
		st.workEntries[objectID] = EntryProof{
			IndexKey:    value,
			ObjectID:    objectID,
			Property:    st.spec.property,
			Value:       value,
			ObjectVer:   ver,
			SourceLSN:   lsn,
			SourceRange: RangeDelta,
		}
	}
}

// upsertLiveEntryLocked 以“按对象覆盖”语义更新存活审计：同一对象该属性
// 的新值替换旧值，使 (值 -> 对象集合) 始终等于对象当前状态。
func (m *IndexManager) upsertLiveEntryLocked(st *indexState, objectID string,
	value PropertyValue, lsn, ver int64) {
	for i, e := range st.audit.Entries {
		if e.ObjectID == objectID {
			st.audit.Entries[i] = EntryProof{
				IndexKey: value, ObjectID: objectID, Property: st.spec.property,
				Value: value, ObjectVer: ver, SourceLSN: lsn, SourceRange: RangeDelta,
			}
			return
		}
	}
	st.audit.Entries = append(st.audit.Entries, EntryProof{
		IndexKey: value, ObjectID: objectID, Property: st.spec.property,
		Value: value, ObjectVer: ver, SourceLSN: lsn, SourceRange: RangeDelta,
	})
}

// FailNextRebuild 注入一次重建故障。
//
// afterBaseline=true：基线扫描完成、应用增量前失败；
// afterBaseline=false：最终封存前失败。两种情况下任何部分条目
// 都不得对外提供查询，状态整体迁移为 failed。
func (m *IndexManager) FailNextRebuild(afterBaseline bool) {
	m.store.Lock()
	defer m.store.Unlock()
	m.pendingFailBaseline = afterBaseline
	m.pendingFailSet = true
}

// snapshotAtOrCurrentLocked 返回对象在 fence 时刻的快照与版本。
// fence 之后才创建的对象在 fence 时刻不存在，返回空快照。
func (m *IndexManager) snapshotAtOrCurrentLocked(oid string, fence int64) (ObjectState, int64) {
	return m.store.snapshotAtLocked(oid, fence)
}

// sourceLSNLocked 取对象某属性历史中 <= fence 的最后一次写入 LSN，
// 作为基线条目的来源写入；不存在则返回 0。
func (m *IndexManager) sourceLSNLocked(oid, property string, fence int64) int64 {
	obj := m.store.objects[oid]
	if obj == nil {
		return 0
	}
	h := obj.hists[property]
	if h == nil {
		return 0
	}
	idx := sort.Search(len(h.lsns), func(i int) bool { return h.lsns[i] > fence }) - 1
	if idx < 0 {
		return 0
	}
	return h.lsns[idx]
}

// abortLocked 丢弃全部在途工作条目并迁移为 failed；此前任何已发布的
// 旧索引保持不变（失败只影响本次重建的结果）。
func (m *IndexManager) abortLocked(st *indexState, _ string) {
	st.building = false
	st.workEntries = nil
	st.phase = PhaseFailed
}

// Rebuild 发起并完成一次索引重建；全部阶段在明确的 LSN 围栏内推进。
//
// 串行等价性：发起围栏 fence 与 Store LSN 分配在同一把锁上取得，
// 因此任何一次写入的 LSN 要么 <= fence（必属基线），要么 > fence（必属
// 增量），不存在“卡在边界上”的写入，基线与增量不重叠、不遗漏对象。
func (m *IndexManager) Rebuild(objectType, indexName string) (*AuditRecord, error) {
	m.store.Lock()
	st, spec, ok := m.stateLocked(objectType, indexName)
	failAfterBaseline := false
	failBeforeSeal := false
	if ok && m.pendingFailSet {
		if m.pendingFailBaseline {
			failAfterBaseline = true
		} else {
			failBeforeSeal = true
		}
		m.pendingFailSet = false
	}
	if !ok {
		m.store.Unlock()
		m.log.Write(DecisionRecord{
			Kind:     KindRebuild,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName},
			Output:   map[string]interface{}{"error": ErrIndexNotDeclared.Error()},
			Basis:    "priority 1: declaration lookup precedes any state transition; nothing mutated",
			Decision: "rejected_index_not_declared",
		})
		return nil, fmt.Errorf("%w: %s/%s", ErrIndexNotDeclared, objectType, indexName)
	}
	if st.building {
		m.store.Unlock()
		return nil, fmt.Errorf("%w: rebuild already in progress for %s/%s",
			ErrIndexUnavailable, objectType, indexName)
	}

	// 1) 基准点：在串行点读取 nextLSN 作为围栏。
	fence := m.store.NextLSN()
	st.building = true
	st.phase = PhaseRebuilding
	st.baselineEndLSN = fence
	st.workEntries = make(map[string]EntryProof)

	// 2) 基线扫描：fence 时刻（含）该类型全部对象。扫描期间到达的写入
	//    LSN 必 > fence，由并发路径上的 applyWriteLocked 记入增量工作集。
	for _, oid := range m.store.objectIDsOfTypeLocked(objectType) {
		snap, ver := m.snapshotAtOrCurrentLocked(oid, fence)
		if value, exists := snap[spec.property]; exists {
			st.workEntries[oid] = EntryProof{
				IndexKey:    value,
				ObjectID:    oid,
				Property:    spec.property,
				Value:       value,
				ObjectVer:   ver,
				SourceLSN:   m.sourceLSNLocked(oid, spec.property, fence),
				SourceRange: RangeBaseline,
			}
		}
	}

	if failAfterBaseline {
		m.abortLocked(st, "injected failure after baseline scan")
		m.store.Unlock()
		m.log.Write(DecisionRecord{
			Kind:     KindRebuild,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName, "fenceLSN": fence},
			Output:   map[string]interface{}{"phase": PhaseFailed},
			Basis:    "no complete audit record sealed; partial entries discarded and never published",
			Decision: "aborted_partial_entries_discarded",
		})
		return nil, fmt.Errorf("%w: rebuild failed after baseline for %s/%s",
			ErrIndexUnavailable, objectType, indexName)
	}

	// 3) 增量：扫描期间写入已按 LSN 顺序累积并覆盖进 workEntries，
	//    此处取封存时点末尾 LSN；failBeforeSeal 模拟封存前失败。
	completionLSN := m.store.nextLSN
	if failBeforeSeal {
		st.completionLSN = completionLSN
		m.abortLocked(st, "injected failure before seal")
		m.store.Unlock()
		return nil, fmt.Errorf("%w: rebuild failed before seal for %s/%s",
			ErrIndexUnavailable, objectType, indexName)
	}
	st.completionLSN = completionLSN

	audit := &AuditRecord{
		IndexName:        spec.name,
		ObjectType:       spec.objectType,
		Property:         spec.property,
		BaselineStartLSN: 1,
		BaselineEndLSN:   fence,
		CompletionLSN:    completionLSN,
	}
	for _, e := range st.workEntries {
		audit.Entries = append(audit.Entries, e)
	}
	audit.Seal()

	// 4) 原子发布：只有完整审计封存成功后才迁移到 active。
	st.audit = audit
	st.phase = PhaseActive
	st.building = false
	st.workEntries = nil
	st.flaggedMismatch = false
	st.flagReason = ""
	m.store.Unlock()

	m.log.Write(DecisionRecord{
		Kind:  KindRebuild,
		Input: map[string]interface{}{"objectType": objectType, "indexName": indexName},
		Output: map[string]interface{}{
			"baselineEndLSN": audit.BaselineEndLSN,
			"completionLSN":  audit.CompletionLSN,
			"entries":        len(audit.Entries),
			"digest":         audit.Digest,
		},
		Basis:    "fence=nextLSN at serialization point; baseline LSN<=fence, delta LSN>fence; sealed audit published atomically",
		Decision: "completed_and_published",
	})
	return audit, nil
}

// Phase 返回索引当前阶段。未声明时返回 PhaseAbsent 且 ok=false。
func (m *IndexManager) Phase(objectType, indexName string) (IndexPhase, bool) {
	m.store.Lock()
	defer m.store.Unlock()
	st, _, ok := m.stateLocked(objectType, indexName)
	if !ok {
		return PhaseAbsent, false
	}
	return st.phase, true
}

// Audit 返回当前封存（含增量追加）的审计记录副本；
// rebuilding/failed 下返回 ErrIndexUnavailable，未声明返回 ErrIndexNotDeclared。
func (m *IndexManager) Audit(objectType, indexName string) (*AuditRecord, IndexPhase, error) {
	m.store.Lock()
	defer m.store.Unlock()
	st, _, ok := m.stateLocked(objectType, indexName)
	if !ok {
		return nil, PhaseAbsent,
			fmt.Errorf("%w: %s/%s", ErrIndexNotDeclared, objectType, indexName)
	}
	if st.phase != PhaseActive || st.audit == nil {
		return nil, st.phase,
			fmt.Errorf("%w: phase=%s for %s/%s",
				ErrIndexUnavailable, st.phase, objectType, indexName)
	}
	cp := *st.audit
	entries := make([]EntryProof, len(st.audit.Entries))
	copy(entries, st.audit.Entries)
	cp.Entries = entries
	return &cp, PhaseActive, nil
}

// Query 按值查询索引，严格执行拒绝优先级：
//
//  1. 未声明 -> ErrIndexNotDeclared；
//  2. rebuilding/failed（无完整审计） -> ErrIndexUnavailable，
//     与“从未存在”的 absent 明确区分；
//  3. active 但曾复核出未修复不一致 -> 正常返回结果，同时置
//     InconsistencyDeclared=true 并给出原因。
func (m *IndexManager) Query(objectType, indexName, value string) (*QueryResult, error) {
	m.store.Lock()
	st, _, ok := m.stateLocked(objectType, indexName)
	if !ok {
		m.store.Unlock()
		m.log.Write(DecisionRecord{
			Kind:     KindQuery,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName, "value": value},
			Output:   map[string]interface{}{"error": ErrIndexNotDeclared.Error()},
			Basis:    "priority 1: index declaration lookup",
			Decision: "rejected_not_declared",
		})
		return nil, fmt.Errorf("%w: %s/%s", ErrIndexNotDeclared, objectType, indexName)
	}
	phase := st.phase
	if phase != PhaseActive || st.audit == nil {
		reason := "index does not exist yet"
		if phase == PhaseRebuilding || phase == PhaseFailed {
			reason = "rebuild in progress or last rebuild aborted without complete audit record"
		}
		m.store.Unlock()
		m.log.Write(DecisionRecord{
			Kind:     KindQuery,
			Input:    map[string]interface{}{"objectType": objectType, "indexName": indexName, "value": value},
			Output:   map[string]interface{}{"phase": string(phase), "reason": reason},
			Basis:    "priority 2: no complete audit record => index unavailable (distinct from not-declared)",
			Decision: "rejected_unavailable",
		})
		err := fmt.Errorf("%w: %s/%s", ErrIndexUnavailable, objectType, indexName)
		if phase == PhaseAbsent {
			err = fmt.Errorf("%w: %s/%s (never built)", ErrIndexUnavailable, objectType, indexName)
		}
		return nil, err
	}

	var ids []string
	for _, e := range st.audit.Entries {
		if e.IndexKey == value {
			ids = append(ids, e.ObjectID)
		}
	}
	sort.Strings(ids)
	res := &QueryResult{
		ObjectIDs:             ids,
		InconsistencyDeclared: st.flaggedMismatch,
		InconsistencyReason:   st.flagReason,
		AuditBaselineEndLSN:   st.audit.BaselineEndLSN,
		AuditCompletionLSN:    st.audit.CompletionLSN,
	}
	declared := st.flaggedMismatch
	reason := st.flagReason
	m.store.Unlock()

	decision := "served_clean"
	if declared {
		decision = "served_with_inconsistency_declaration"
	}
	m.log.Write(DecisionRecord{
		Kind: KindQuery,
		Input: map[string]interface{}{
			"objectType": objectType, "indexName": indexName, "value": value,
		},
		Output: map[string]interface{}{
			"objectIDs": ids, "inconsistencyDeclared": declared, "reason": reason,
		},
		Basis:    "priority 3: active index serves results; unrepaired mismatches are declared, not hidden",
		Decision: decision,
	})
	return res, nil
}

// FlagInconsistent 由复核器在发现条目级不一致后调用，标记索引需修复。
// 标记不改变任何索引条目内容，仅改变查询结果上的声明位。
func (m *IndexManager) FlagInconsistent(objectType, indexName, reason string) {
	m.store.Lock()
	defer m.store.Unlock()
	if st, _, ok := m.stateLocked(objectType, indexName); ok && st.phase == PhaseActive {
		st.flaggedMismatch = true
		st.flagReason = reason
	}
}

// ClearInconsistencyFlag 在一次新的成功重建后清除标记（Rebuild 内部已调用）。
func (m *IndexManager) ClearInconsistencyFlag(objectType, indexName string) {
	m.store.Lock()
	defer m.store.Unlock()
	if st, _, ok := m.stateLocked(objectType, indexName); ok {
		st.flaggedMismatch = false
		st.flagReason = ""
	}
}

// TamperEntryForTest 供测试人为篡改某个对象对应的审计条目（模拟存储层
// 比特损坏/串值），以验证复核不依赖重建执行日志也能独立发现不一致。
// 返回被篡改条目的旧值。
func (m *IndexManager) TamperEntryForTest(objectType, indexName, objectID,
	forgedValue string) (PropertyValue, bool) {
	m.store.Lock()
	defer m.store.Unlock()
	st, _, ok := m.stateLocked(objectType, indexName)
	if !ok || st.audit == nil {
		return "", false
	}
	for i, e := range st.audit.Entries {
		if e.ObjectID == objectID {
			old := e.Value
			e.IndexKey = forgedValue
			e.Value = forgedValue
			st.audit.Entries[i] = e
			// 故意不重算摘要，模拟与对象状态脱节的脏条目。
			return old, true
		}
	}
	return "", false
}
