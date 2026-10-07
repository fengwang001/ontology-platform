package ontologyindex

import "sync"

// indexStatus 是单个索引版本状态机的相位。
type indexStatus int

const (
	statusActive indexStatus = iota
	statusSwitching
	statusDeprecated
)

// indexSpec 描述一个受维护的索引：对象类型上的某个稳定逻辑属性。
type indexSpec struct {
	id         string
	objectType string
	propertyID string
	constraint IndexConstraint
}

// indexMaterialization 是某一索引版本的物化结果。
//
// 不变量（同一版本内部始终成立）：版本先在隔离副本上构建并校验，通过后
// 才整体发布；查询端只能拿到某个完整版本，不存在新旧依据混杂的中间态。
type indexMaterialization struct {
	version   int64
	cutoverAt LogicalClock
	includeAt func(LogicalClock) bool

	// valueToObjects：取值 -> 对象集合。查询只做一次哈希定位，
	// 步数与累计处理过的事件总数无关（平均 O(1)）。
	valueToObjects map[Value]*sortedObjectSet
	objectToValue  map[string]Value
	objectToTS     map[string]LogicalClock
	objectToEvent  map[string]string
}

// indexState 是一个索引的运行时状态。
type indexState struct {
	spec             indexSpec
	status           indexStatus
	active           *indexMaterialization
	previous         *indexMaterialization
	switchCutover    LogicalClock
	committedVersion int64

	// log 保留去重后的全部原始事件；提交/回滚都从同一事实集合确定性
	// 重建目标版本，是“回滚不丢已缓冲事件”的物理基础。
	log  []ChangeEvent
	seen map[string]struct{}
}

const minClock = LogicalClock(-1 << 62)

// Engine 是索引增量维护子系统的并发安全入口。
//
// 并发模型：单一读写互斥锁把 Ingest / BeginSwitch / CommitSwitch /
// AbortSwitch / DeprecateIndex / Lookup 线性化，每次调用在锁内原子生效，
// 任意并发操作的可观察结果都等价于某一全局顺序下的串行执行。
type Engine struct {
	mu      sync.RWMutex
	schema  *Schema
	auditor Auditor
	indexes map[string]*indexState
	arrival int64
}

// NewEngine 创建引擎。
func NewEngine(s *Schema, auditor Auditor) *Engine {
	if auditor == nil {
		auditor = discardAuditor{}
	}
	return &Engine{schema: s, auditor: auditor, indexes: map[string]*indexState{}}
}

type discardAuditor struct{}

func (discardAuditor) Record(AuditRecord) {}

// CreateIndex 创建索引。
func (e *Engine) CreateIndex(id, objectType, propertyID string, c IndexConstraint) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.indexes[id]; exists {
		return errValidation("索引 %q 已存在", id)
	}
	st := &indexState{
		spec:             indexSpec{id: id, objectType: objectType, propertyID: propertyID, constraint: c},
		status:           statusActive,
		active:           newMaterialization(1, minClock, func(LogicalClock) bool { return true }),
		committedVersion: 1,
		seen:             map[string]struct{}{},
	}
	e.indexes[id] = st
	e.audit(AuditRecord{Op: "create_index", IndexID: id, IndexVersion: 1, Decision: "created",
		Detail: map[string]any{"object_type": objectType, "property": propertyID}})
	return nil
}

func newMaterialization(version int64, cutover LogicalClock, include func(LogicalClock) bool) *indexMaterialization {
	return &indexMaterialization{
		version:        version,
		cutoverAt:      cutover,
		includeAt:      include,
		valueToObjects: map[Value]*sortedObjectSet{},
		objectToValue:  map[string]Value{},
		objectToTS:     map[string]LogicalClock{},
		objectToEvent:  map[string]string{},
	}
}

// sortedObjectSet 是一个维护有序去重对象列表的集合。
// 增删为 O(k)（k=该取值桶大小，与累计事件总数无关）；
// 查询直接返回有序切片拷贝，不在查询路径上排序。
type sortedObjectSet struct {
	m map[string]struct{}
	s []string
}

func newSortedObjectSet() *sortedObjectSet {
	return &sortedObjectSet{m: map[string]struct{}{}}
}

func (s *sortedObjectSet) add(id string) {
	if _, ok := s.m[id]; ok {
		return
	}
	s.m[id] = struct{}{}
	pos := 0
	for pos < len(s.s) && s.s[pos] < id {
		pos++
	}
	s.s = append(s.s, "")
	copy(s.s[pos+1:], s.s[pos:])
	s.s[pos] = id
}

func (s *sortedObjectSet) remove(id string) {
	if _, ok := s.m[id]; !ok {
		return
	}
	delete(s.m, id)
	for i, v := range s.s {
		if v == id {
			s.s = append(s.s[:i], s.s[i+1:]...)
			return
		}
	}
}

// laterThan 定义合法串行顺序：先按 EffectiveAt（逻辑生效时刻）升序；
// 同一逻辑时刻以 EventID 字典序打破平局。物理到达顺序绝不参与判定。
func laterThan(ts LogicalClock, eventID string, curTS LogicalClock, curID string) bool {
	if ts != curTS {
		return ts > curTS
	}
	return eventID > curID
}

func (m *indexMaterialization) putObject(obj string, v Value, ts LogicalClock, eventID string) {
	if old, existed := m.objectToValue[obj]; existed {
		m.valueToObjects[old].remove(obj)
		if len(m.valueToObjects[old].s) == 0 {
			delete(m.valueToObjects, old)
		}
	}
	m.objectToValue[obj] = v
	m.objectToTS[obj] = ts
	m.objectToEvent[obj] = eventID
	set := m.valueToObjects[v]
	if set == nil {
		set = newSortedObjectSet()
		m.valueToObjects[v] = set
	}
	set.add(obj)
}

// validateEvent 依据事件生效时刻适用的类型版本判定 E1/E3。
func (e *Engine) validateEvent(spec indexSpec, ev ChangeEvent) error {
	var errs []error
	if _, err := e.schema.resolveProperty(spec.objectType, ev.PropertyID, ev.EffectiveAt); err != nil {
		errs = append(errs, err)
	}
	if _, v, ok := e.schema.resolveField(spec.objectType, ev.PropertyID, ev.EffectiveAt); !ok {
		if v != nil {
			errs = append(errs, errUndefined("事件 %s 引用的属性 %q 在类型 %q 于逻辑时刻 %d 生效的版本中尚未定义",
				ev.EventID, ev.PropertyID, spec.objectType, ev.EffectiveAt))
		} else {
			errs = append(errs, errUndefined("事件 %s 引用的类型 %q 在逻辑时刻 %d 尚未定义",
				ev.EventID, spec.objectType, ev.EffectiveAt))
		}
	}
	return highestPriorityError(errs)
}

func (e *Engine) audit(r AuditRecord) {
	if r.Detail == nil {
		r.Detail = map[string]any{}
	}
	e.auditor.Record(r)
}

func (e *Engine) getIndex(id string) (*indexState, error) {
	st, ok := e.indexes[id]
	if !ok {
		return nil, errValidation("索引 %q 不存在", id)
	}
	return st, nil
}

// Ingest 消费一条变更事件（乱序、重复、跨切换均安全）。
func (e *Engine) Ingest(ev ChangeEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.arrival++
	if ev.ArrivedAt == 0 {
		ev.ArrivedAt = e.arrival
	}
	if !ev.valid() {
		return errUndefined("事件字段不完备: %+v", ev)
	}

	targets := e.routeEvent(ev)
	if len(targets) == 0 {
		e.audit(AuditRecord{Op: "ingest", EventID: ev.EventID, ArrivedAt: ev.ArrivedAt,
			EffectiveAt: ev.EffectiveAt, Decision: "ignored_no_index"})
		return nil
	}

	var firstErr error
	for _, st := range targets {
		if err := e.ingestOne(st, ev); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// routeEvent 按“索引属性血缘”路由：改名/替换不改变逻辑属性身份，
// 新旧两个物理字段上的事件都归入同一条索引。
func (e *Engine) routeEvent(ev ChangeEvent) []*indexState {
	var out []*indexState
	for _, st := range e.indexes {
		if st.spec.objectType != ev.ObjectType {
			continue
		}
		lineage := e.schema.lineageOf(st.spec.objectType, st.spec.propertyID)
		if _, ok := lineage[ev.PropertyID]; ok {
			out = append(out, st)
		}
	}
	return out
}

func (e *Engine) ingestOne(st *indexState, ev ChangeEvent) error {
	rec := AuditRecord{Op: "ingest", EventID: ev.EventID, ArrivedAt: ev.ArrivedAt,
		EffectiveAt: ev.EffectiveAt, IndexID: st.spec.id, IndexVersion: st.committedVersion}

	if _, dup := st.seen[ev.EventID]; dup {
		rec.Decision = "duplicate_ignored"
		e.audit(rec)
		return nil
	}

	if st.status == statusDeprecated {
		err := errDeprecated("索引 %q 的依据属性已废弃且无替代字段，事件 %s 无法归入任何版本",
			st.spec.id, ev.EventID)
		rec.Decision = "rejected_deprecated"
		rec.ErrorCode = ErrDeprecatedProperty
		e.audit(rec)
		return err
	}

	if err := e.validateEvent(st.spec, ev); err != nil {
		rec.Decision = "rejected_invalid"
		if ie, ok := err.(*IndexError); ok {
			rec.ErrorCode = ie.Code
		}
		e.audit(rec)
		return err
	}

	st.seen[ev.EventID] = struct{}{}
	st.log = append(st.log, ev)

	switch {
	case st.status == statusSwitching:
		// 切换进行中：查询关闭（E4），事件只缓冲，绝不触碰活跃旧版本。
		rec.Decision = "buffered_during_switch"
	case st.active.includeAt(ev.EffectiveAt):
		curTS, has := st.active.objectToTS[ev.ObjectID]
		curID := ""
		if has {
			curID = st.active.objectToEvent[ev.ObjectID]
		}
		if !has || laterThan(ev.EffectiveAt, ev.EventID, curTS, curID) {
			st.active.putObject(ev.ObjectID, ev.NewValue, ev.EffectiveAt, ev.EventID)
		}
		rec.Decision = "accepted"
	default:
		// 切换提交后迟到的 ts<cutover 旧字段事件：仅存事实日志，不污染新版本。
		rec.Decision = "accepted_historical_only"
	}
	e.audit(rec)
	return nil
}

func sortEvents(events []ChangeEvent) {
	insertionSortEvents(events)
}

func insertionSortEvents(a []ChangeEvent) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			if a[j-1].EffectiveAt < a[j].EffectiveAt ||
				(a[j-1].EffectiveAt == a[j].EffectiveAt && a[j-1].EventID < a[j].EventID) {
				break
			}
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// materialize 从全量事件日志构建隔离版本：取 include 区间内、
// 按 (EffectiveAt, EventID) 排序后每对象的最后取值。
func (e *Engine) materialize(st *indexState, version int64, cutover LogicalClock, include func(LogicalClock) bool) *indexMaterialization {
	m := newMaterialization(version, cutover, include)
	events := append([]ChangeEvent(nil), st.log...)
	sortEvents(events)
	for _, ev := range events {
		if !include(ev.EffectiveAt) {
			continue
		}
		m.putObject(ev.ObjectID, ev.NewValue, ev.EffectiveAt, ev.EventID)
	}
	return m
}

// validateConstraint 校验新字段版本是否满足索引约束（唯一性）。
func validateConstraint(spec indexSpec, m *indexMaterialization) error {
	if spec.constraint != ConstraintUnique {
		return nil
	}
	for v, set := range m.valueToObjects {
		if len(set.s) > 1 {
			return errValidation("新字段取值 %v 对应 %d 个对象，违反唯一索引约束", v, len(set.s))
		}
	}
	return nil
}

// BeginSwitch 宣布索引依据字段切换点 cutoverAt：逻辑时刻 >= cutoverAt
// 的事件属于新字段版本，之前的属于旧字段版本。归属只看生效时刻，与物理
// 到达时刻无关。切换期间查询返回 ErrSwitchInProgress。
func (e *Engine) BeginSwitch(indexID string, cutoverAt LogicalClock) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getIndex(indexID)
	if err != nil {
		return err
	}
	if st.status != statusActive {
		return errInProgress("索引 %q 当前相位不允许开始切换", indexID)
	}
	st.status = statusSwitching
	st.switchCutover = cutoverAt
	e.audit(AuditRecord{Op: "begin_switch", IndexID: indexID, IndexVersion: st.committedVersion,
		Decision: "switching_started", Detail: map[string]any{"cutover_at": int64(cutoverAt)}})
	return nil
}

// CommitSwitch 完成切换：隔离构建新版本并校验，通过后原子发布；
// 校验失败（E2）整体回滚，缓冲事件重归入旧版本，不丢失。
func (e *Engine) CommitSwitch(indexID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getIndex(indexID)
	if err != nil {
		return err
	}
	if st.status != statusSwitching {
		return errValidation("索引 %q 不处于切换中，无法提交", indexID)
	}

	cut := st.switchCutover
	cand := e.materialize(st, st.committedVersion+1, cut, func(ts LogicalClock) bool { return ts >= cut })

	if vErr := validateConstraint(st.spec, cand); vErr != nil {
		old := e.materialize(st, st.committedVersion, minClock, func(LogicalClock) bool { return true })
		st.active = old
		st.status = statusActive
		st.switchCutover = 0
		e.audit(AuditRecord{Op: "commit_switch", IndexID: indexID, IndexVersion: st.committedVersion,
			Decision: "rolled_back_validation_failed", ErrorCode: ErrSwitchValidation,
			Detail: map[string]any{"reason": vErr.Error()}})
		return errValidation("索引 %q 切换校验失败，已整体回滚: %v", indexID, vErr)
	}

	oldVersion := st.committedVersion
	st.previous = st.active
	st.active = cand
	st.status = statusActive
	st.committedVersion = cand.version
	st.switchCutover = 0
	e.audit(AuditRecord{Op: "commit_switch", IndexID: indexID, IndexVersion: cand.version,
		Decision: "committed", Detail: map[string]any{"old_version": oldVersion, "new_version": cand.version}})
	return nil
}

// AbortSwitch 放弃切换：丢弃未提交的新版本，从同一事件日志重建旧版本，
// 切换窗口内缓冲的新、旧字段事件全部重归入旧版本继续生效。
func (e *Engine) AbortSwitch(indexID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getIndex(indexID)
	if err != nil {
		return err
	}
	if st.status != statusSwitching {
		return errValidation("索引 %q 不处于切换中，无法中止", indexID)
	}
	old := e.materialize(st, st.committedVersion, minClock, func(LogicalClock) bool { return true })
	st.active = old
	st.status = statusActive
	st.switchCutover = 0
	e.audit(AuditRecord{Op: "abort_switch", IndexID: indexID, IndexVersion: st.committedVersion,
		Decision: "aborted_old_version_restored",
		Detail:   map[string]any{"buffered_events_reapplied": len(st.log)}})
	return nil
}

// DeprecateIndex 声明索引依据属性已无替代字段地废弃（E1 状态）。
// 此后查询与事件归入均报告 ErrDeprecatedProperty。
func (e *Engine) DeprecateIndex(indexID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, err := e.getIndex(indexID)
	if err != nil {
		return err
	}
	st.status = statusDeprecated
	e.audit(AuditRecord{Op: "deprecate_index", IndexID: indexID, IndexVersion: st.committedVersion,
		Decision: "deprecated"})
	return nil
}

// Lookup 按属性取值定位对象。切换进行中（E4）或索引已废弃（E1）时报错；
// 正常态仅对 active 版本做一次哈希定位，不扫描任何事件。
func (e *Engine) Lookup(indexID string, v Value) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, err := e.getIndex(indexID)
	if err != nil {
		return nil, err
	}
	rec := AuditRecord{Op: "lookup", IndexID: indexID, IndexVersion: st.committedVersion,
		Detail: map[string]any{}}
	switch st.status {
	case statusDeprecated:
		rec.Decision = "rejected_deprecated"
		rec.ErrorCode = ErrDeprecatedProperty
		e.audit(rec)
		return nil, errDeprecated("索引 %q 的依据属性已废弃且无替代字段", indexID)
	case statusSwitching:
		rec.Decision = "rejected_switch_in_progress"
		rec.ErrorCode = ErrSwitchInProgress
		e.audit(rec)
		return nil, errInProgress("索引 %q 正处于依据字段切换中，请稍后重试", indexID)
	}
	set := st.active.valueToObjects[v]
	var out []string
	if set != nil {
		out = append(out, set.s...)
	}
	rec.Decision = "returned"
	rec.Detail["result_count"] = len(out)
	e.audit(rec)
	return out, nil
}

// Status 返回索引当前相位与版本号（供测试与可观测性使用）。
func (e *Engine) Status(indexID string) (status string, version int64, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, gerr := e.getIndex(indexID)
	if gerr != nil {
		return "", 0, gerr
	}
	switch st.status {
	case statusSwitching:
		status = "switching"
	case statusDeprecated:
		status = "deprecated"
	default:
		status = "active"
	}
	return status, st.committedVersion, nil
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
