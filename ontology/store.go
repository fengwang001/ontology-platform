package ontology

import (
	"sort"
	"sync"
	"sync/atomic"
)

// PropertyDef 是被索引属性的声明：属性 ID + 若干索引结构（各有独立键生成规则）。
type PropertyDef struct {
	ID      string
	Indexes []*Index
}

// instance 是对象实例的运行时表示。
type instance struct {
	mu sync.RWMutex
	// props 按属性 ID 存值；不同属性的并发写入互不阻塞，
	// 因此不能用普通 map（并发写不同 key 也会竞争）。
	props   sync.Map
	deleted bool
}

func (in *instance) getProp(propertyID string) Value {
	if v, ok := in.props.Load(propertyID); ok {
		return v.(Value)
	}
	return Absent
}

func (in *instance) setProp(propertyID string, v Value) {
	in.props.Store(propertyID, v)
}

// Store 是属性索引一致性子系统的核心。
type Store struct {
	mu        sync.RWMutex
	instances map[string]*instance
	props     map[string]*PropertyDef

	// applyMu：写入/删除的应用段持读锁（彼此不阻塞），查询持写锁，
	// 使查询等价于发生在某个与写入次序一致的时间点。
	applyMu sync.RWMutex
	// propLocks 按 (实例, 属性) 粒度串行化同一属性的并发写入。
	propLocks sync.Map

	wal    WAL
	faults *FaultInjector
	log    Logger
	clock  atomic.Uint64
}

// NewStore 构造存储。faults 可为 nil（不注入故障）。
func NewStore(wal WAL, faults *FaultInjector, logger Logger) *Store {
	if wal == nil {
		wal = NewMemoryWAL()
	}
	if faults == nil {
		faults = NewFaultInjector()
	}
	if logger == nil {
		logger = nopLogger{}
	}
	return &Store{
		instances: make(map[string]*instance),
		props:     make(map[string]*PropertyDef),
		wal:       wal,
		faults:    faults,
		log:       logger,
	}
}

// RegisterProperty 声明一个被索引属性及其索引结构（可多个，键规则各异）。
// 已存在的实例会以 AbsentKey 回填所有新索引，保证"不存在"状态可查询。
func (s *Store) RegisterProperty(propertyID string, indexes ...*Index) {
	s.mu.Lock()
	s.props[propertyID] = &PropertyDef{ID: propertyID, Indexes: indexes}
	insts := make(map[string]*instance, len(s.instances))
	for id, inst := range s.instances {
		insts[id] = inst
	}
	s.mu.Unlock()
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for id, inst := range insts {
		inst.mu.RLock()
		if !inst.deleted {
			for _, ix := range indexes {
				ix.add(AbsentKey, id)
			}
		}
		inst.mu.RUnlock()
	}
}

// AddInstance 创建对象实例。
func (s *Store) AddInstance(id string) {
	s.mu.Lock()
	s.instances[id] = &instance{}
	defs := make([]*PropertyDef, 0, len(s.props))
	for _, def := range s.props {
		defs = append(defs, def)
	}
	s.mu.Unlock()
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for _, def := range defs {
		for _, ix := range def.Indexes {
			ix.add(AbsentKey, id)
		}
	}
}

// Clock 返回逻辑时钟戳；只有成功提交的处理单元才会推进时钟。
func (s *Store) Clock() uint64 { return s.clock.Load() }

func (s *Store) getInstance(id string) *instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.instances[id]
}

func (s *Store) propDef(propertyID string) *PropertyDef {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.props[propertyID]
}

func (s *Store) propLock(instanceID, propertyID string) *sync.Mutex {
	key := instanceID + "\x00" + propertyID
	l, _ := s.propLocks.LoadOrStore(key, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// InstanceWrite 是批量写入中单个实例的写入项。
type InstanceWrite struct {
	InstanceID string
	Value      Value
}

// makeUndo 读取实例当前属性值并构造 undo 记录（调用方须持有实例锁）。
func makeUndo(inst *instance, instID string, def *PropertyDef, newVal Value) UndoEntry {
	old := inst.getProp(def.ID)
	e := UndoEntry{
		InstanceID: instID,
		PropertyID: def.ID,
		OldValue:   old,
		NewValue:   newVal,
		OldKeys:    make([]Key, len(def.Indexes)),
		NewKeys:    make([]Key, len(def.Indexes)),
	}
	for i, ix := range def.Indexes {
		e.OldKeys[i] = ix.KeyFn(old)
		e.NewKeys[i] = ix.KeyFn(newVal)
	}
	return e
}

// forwardEntry 幂等前滚：属性值设为 NewValue，索引条目从 OldKeys 换到 NewKeys。
// NewKeys 为 nil 表示删除语义（只移除旧条目）。
func forwardEntry(inst *instance, def *PropertyDef, e *UndoEntry, log Logger) {
	if inst != nil {
		inst.setProp(def.ID, e.NewValue)
	}
	for i, ix := range def.Indexes {
		ix.remove(e.OldKeys[i], e.InstanceID)
		log.Logf("INDEX- txn-apply index=%s key=%v instance=%s", ix.ID, e.OldKeys[i], e.InstanceID)
		if e.NewKeys != nil {
			ix.add(e.NewKeys[i], e.InstanceID)
			log.Logf("INDEX+ txn-apply index=%s key=%v instance=%s", ix.ID, e.NewKeys[i], e.InstanceID)
		}
	}
}

// revertEntry 幂等回滚：属性值与全部索引条目恢复到写入前状态。
func revertEntry(inst *instance, def *PropertyDef, e *UndoEntry, log Logger) {
	if inst != nil {
		inst.setProp(def.ID, e.OldValue)
	}
	for i, ix := range def.Indexes {
		if e.NewKeys != nil {
			ix.remove(e.NewKeys[i], e.InstanceID)
			log.Logf("INDEX- txn-revert index=%s key=%v instance=%s", ix.ID, e.NewKeys[i], e.InstanceID)
		}
		ix.add(e.OldKeys[i], e.InstanceID)
		log.Logf("INDEX+ txn-revert index=%s key=%v instance=%s", ix.ID, e.OldKeys[i], e.InstanceID)
	}
}

// applyEntryLive 正常路径前滚一个条目，带切分点崩溃注入与索引维护失败处理。
// 失败时把该条目已应用的部分回滚，返回 ErrIndexMaintenance。
// 调用方须持有实例锁与 applyMu.RLock。
func (s *Store) applyEntryLive(txnID uint64, inst *instance, def *PropertyDef, e *UndoEntry) error {
	inst.setProp(def.ID, e.NewValue)
	s.faults.maybeCrash(CutAfterValue, txnID, "")
	for i, ix := range def.Indexes {
		if s.faults.shouldFailIndex(ix.ID, e.InstanceID) {
			// 索引维护失败：回滚该条目已应用的部分（属性值 + 前 i 个索引）。
			inst.setProp(def.ID, e.OldValue)
			for j := 0; j < i; j++ {
				prev := def.Indexes[j]
				prev.remove(e.NewKeys[j], e.InstanceID)
				prev.add(e.OldKeys[j], e.InstanceID)
			}
			s.log.Logf("ROLLBACK-PARTIAL txn=%d instance=%s property=%s failed-index=%s",
				txnID, e.InstanceID, def.ID, ix.ID)
			return &OpError{Kind: ErrIndexMaintenance, InstanceID: e.InstanceID,
				PropertyID: def.ID, IndexID: ix.ID, Detail: "index rejected the entry"}
		}
		ix.remove(e.OldKeys[i], e.InstanceID)
		ix.add(e.NewKeys[i], e.InstanceID)
		s.log.Logf("INDEX- txn=%d index=%s key=%v instance=%s", txnID, ix.ID, e.OldKeys[i], e.InstanceID)
		s.log.Logf("INDEX+ txn=%d index=%s key=%v instance=%s", txnID, ix.ID, e.NewKeys[i], e.InstanceID)
		s.faults.maybeCrash(CutAfterIndex, txnID, ix.ID)
	}
	return nil
}

// Write 单实例单属性写入：属性值变更与全部相关索引条目增删构成同一处理单元。
func (s *Store) Write(instanceID, propertyID string, v Value) error {
	inst := s.getInstance(instanceID)
	if inst == nil {
		return &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID, PropertyID: propertyID}
	}
	def := s.propDef(propertyID)
	if def == nil {
		return &OpError{Kind: ErrPropertyNotIndexable, InstanceID: instanceID, PropertyID: propertyID}
	}
	pl := s.propLock(instanceID, propertyID)
	pl.Lock()
	defer pl.Unlock()
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	if inst.deleted {
		return &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID, PropertyID: propertyID}
	}
	s.log.Logf("WRITE instance=%s property=%s value=%+v", instanceID, propertyID, v)

	undo := makeUndo(inst, instanceID, def, v)
	txnID := s.wal.Begin("write", []UndoEntry{undo})
	s.faults.maybeCrash(CutAfterWALBegin, txnID, "")

	s.applyMu.RLock()
	defer s.applyMu.RUnlock()
	if err := s.applyEntryLive(txnID, inst, def, &undo); err != nil {
		s.wal.End(txnID)
		s.log.Logf("REJECT txn=%d reason=%v", txnID, err)
		return err
	}
	s.faults.maybeCrash(CutAfterAllIndexes, txnID, "")
	clk := s.clock.Add(1)
	s.wal.Commit(txnID, clk)
	s.faults.maybeCrash(CutAfterCommit, txnID, "")
	s.wal.End(txnID)
	s.log.Logf("COMMIT txn=%d clock=%d instance=%s property=%s", txnID, clk, instanceID, propertyID)
	return nil
}

// BatchWrite 同一属性对多个实例的批量写入：任一实例索引维护失败则整体回滚。
// 返回与 writes 等长的逐实例错误切片；整体被拒绝时不改变任何状态与时钟戳。
func (s *Store) BatchWrite(propertyID string, writes []InstanceWrite) []error {
	errs := make([]error, len(writes))
	def := s.propDef(propertyID)
	if def == nil {
		for i, w := range writes {
			errs[i] = &OpError{Kind: ErrPropertyNotIndexable, InstanceID: w.InstanceID, PropertyID: propertyID}
		}
		return errs
	}
	// 同一实例重复出现时以最后一项为准（否则 undo 基于写入前状态，
	// 重复应用会残留中间键）。
	last := make(map[string]int, len(writes))
	for i, w := range writes {
		last[w.InstanceID] = i
	}
	kept := make([]int, 0, len(last))
	for i, w := range writes {
		if last[w.InstanceID] == i {
			kept = append(kept, i)
		}
	}
	if len(kept) != len(writes) {
		deduped := make([]InstanceWrite, len(kept))
		for i, idx := range kept {
			deduped[i] = writes[idx]
		}
		subErrs := s.BatchWrite(propertyID, deduped)
		for i, idx := range kept {
			errs[idx] = subErrs[i]
		}
		// 被覆盖的项与最终生效项同命运。
		for i, w := range writes {
			if last[w.InstanceID] != i {
				errs[i] = errs[last[w.InstanceID]]
			}
		}
		return errs
	}
	// 按实例 ID 排序取锁，避免并发批量写之间死锁。
	order := make([]int, len(writes))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return writes[order[a]].InstanceID < writes[order[b]].InstanceID })

	insts := make([]*instance, len(writes))
	locks := make([]*sync.Mutex, 0, len(writes))
	locked := make(map[string]bool)
	release := func() {
		for _, l := range locks {
			l.Unlock()
		}
		seen := make(map[*instance]bool)
		for _, inst := range insts {
			if inst != nil && seen[inst] {
				continue
			}
			if inst != nil {
				seen[inst] = true
				inst.mu.RUnlock()
			}
		}
	}
	// 校验阶段：任何校验失败都不改变任何状态。
	valid := true
	for _, idx := range order {
		w := writes[idx]
		inst := s.getInstance(w.InstanceID)
		if inst == nil {
			errs[idx] = &OpError{Kind: ErrInstanceNotFound, InstanceID: w.InstanceID, PropertyID: propertyID}
			valid = false
			continue
		}
		if !locked[w.InstanceID] {
			locked[w.InstanceID] = true
			pl := s.propLock(w.InstanceID, propertyID)
			pl.Lock()
			locks = append(locks, pl)
			inst.mu.RLock()
		}
		if inst.deleted {
			errs[idx] = &OpError{Kind: ErrInstanceNotFound, InstanceID: w.InstanceID, PropertyID: propertyID}
			valid = false
		}
		insts[idx] = inst
	}
	defer release()
	if !valid {
		for i := range errs {
			if errs[i] == nil {
				errs[i] = &OpError{Kind: ErrBatchRolledBack, InstanceID: writes[i].InstanceID,
					PropertyID: propertyID, Detail: "batch rejected during validation"}
			}
		}
		s.log.Logf("BATCH-REJECT property=%s reason=validation", propertyID)
		return errs
	}
	for _, w := range writes {
		s.log.Logf("BATCH-WRITE instance=%s property=%s value=%+v", w.InstanceID, propertyID, w.Value)
	}

	undos := make([]UndoEntry, len(writes))
	for i, w := range writes {
		undos[i] = makeUndo(insts[i], w.InstanceID, def, w.Value)
	}
	txnID := s.wal.Begin("batch", undos)
	s.faults.maybeCrash(CutAfterWALBegin, txnID, "")

	s.applyMu.RLock()
	defer s.applyMu.RUnlock()
	for i := range writes {
		if err := s.applyEntryLive(txnID, insts[i], def, &undos[i]); err != nil {
			// 整体回滚：已应用的条目全部恢复到写入前状态。
			for j := 0; j <= i; j++ {
				revertEntry(insts[j], def, &undos[j], s.log)
			}
			s.wal.End(txnID)
			for j := range errs {
				if j == i {
					errs[j] = err
				} else {
					errs[j] = &OpError{Kind: ErrBatchRolledBack, InstanceID: writes[j].InstanceID,
						PropertyID: propertyID, Detail: "rolled back due to another instance"}
				}
			}
			s.log.Logf("BATCH-ROLLBACK txn=%d failed-instance=%s", txnID, writes[i].InstanceID)
			return errs
		}
	}
	s.faults.maybeCrash(CutAfterAllIndexes, txnID, "")
	clk := s.clock.Add(1)
	s.wal.Commit(txnID, clk)
	s.faults.maybeCrash(CutAfterCommit, txnID, "")
	s.wal.End(txnID)
	s.log.Logf("COMMIT txn=%d clock=%d batch-size=%d property=%s", txnID, clk, len(writes), propertyID)
	return errs
}

// DeleteInstance 删除实例：全部被索引属性的索引条目在同一处理单元内清除。
func (s *Store) DeleteInstance(instanceID string) error {
	s.mu.RLock()
	inst := s.instances[instanceID]
	s.mu.RUnlock()
	if inst == nil {
		return &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID}
	}
	s.applyMu.RLock()
	defer s.applyMu.RUnlock()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.deleted {
		return &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID}
	}
	s.log.Logf("DELETE instance=%s", instanceID)

	s.mu.RLock()
	defs := make([]*PropertyDef, 0, len(s.props))
	for _, def := range s.props {
		defs = append(defs, def)
	}
	s.mu.RUnlock()

	undos := make([]UndoEntry, 0, len(defs))
	for _, def := range defs {
		e := makeUndo(inst, instanceID, def, Absent)
		e.NewKeys = nil // 删除语义：只移除旧条目
		undos = append(undos, e)
	}
	txnID := s.wal.Begin("delete", undos)
	s.faults.maybeCrash(CutAfterWALBegin, txnID, "")

	inst.deleted = true
	s.mu.Lock()
	delete(s.instances, instanceID)
	s.mu.Unlock()
	s.faults.maybeCrash(CutAfterValue, txnID, "")
	for i := range undos {
		def := defs[i]
		e := &undos[i]
		for j, ix := range def.Indexes {
			if s.faults.shouldFailIndex(ix.ID, instanceID) {
				// 回滚：恢复实例可见性与全部已清除条目。
				inst.deleted = false
				s.mu.Lock()
				s.instances[instanceID] = inst
				s.mu.Unlock()
				for k := range undos {
					revertEntry(inst, defs[k], &undos[k], s.log)
				}
				s.wal.End(txnID)
				s.log.Logf("DELETE-ROLLBACK txn=%d instance=%s failed-index=%s", txnID, instanceID, ix.ID)
				return &OpError{Kind: ErrIndexMaintenance, InstanceID: instanceID, IndexID: ix.ID}
			}
			ix.remove(e.OldKeys[j], instanceID)
			s.log.Logf("INDEX- txn=%d index=%s key=%v instance=%s", txnID, ix.ID, e.OldKeys[j], instanceID)
			s.faults.maybeCrash(CutAfterIndex, txnID, ix.ID)
		}
	}
	s.faults.maybeCrash(CutAfterAllIndexes, txnID, "")
	clk := s.clock.Add(1)
	s.wal.Commit(txnID, clk)
	s.faults.maybeCrash(CutAfterCommit, txnID, "")
	s.wal.End(txnID)
	s.log.Logf("COMMIT txn=%d clock=%d delete instance=%s", txnID, clk, instanceID)
	return nil
}

// QueryByIndex 用指定索引结构按键查询。
// 命中条目会回读实例当前属性值校验，保证不返回陈旧或幻影结果；
// 查询持 applyMu 写锁，等价于发生在某个与写入次序一致的时间点。
func (s *Store) QueryByIndex(propertyID, indexID string, key Key) ([]string, error) {
	def := s.propDef(propertyID)
	if def == nil {
		return nil, &OpError{Kind: ErrPropertyNotIndexable, PropertyID: propertyID, IndexID: indexID}
	}
	var target *Index
	for _, ix := range def.Indexes {
		if ix.ID == indexID {
			target = ix
			break
		}
	}
	if target == nil {
		return nil, &OpError{Kind: ErrPropertyNotIndexable, PropertyID: propertyID,
			IndexID: indexID, Detail: "no such index on property"}
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	candidates := target.lookup(key)
	out := make([]string, 0, len(candidates))
	for _, id := range candidates {
		inst := s.getInstance(id)
		if inst == nil {
			continue
		}
		inst.mu.RLock()
		alive := !inst.deleted
		match := alive && target.KeyFn(inst.getProp(propertyID)) == key
		inst.mu.RUnlock()
		if match {
			out = append(out, id)
		}
	}
	s.log.Logf("QUERY property=%s index=%s key=%v candidates=%d hits=%d",
		propertyID, indexID, key, len(candidates), len(out))
	return sortedIDs(out), nil
}

// QueryByValue 按属性值查询（使用属性的第一个索引结构）。
// 显式默认值与 Absent 映射到不同的键，查询结果不会混淆。
func (s *Store) QueryByValue(propertyID string, v Value) ([]string, error) {
	def := s.propDef(propertyID)
	if def == nil {
		return nil, &OpError{Kind: ErrPropertyNotIndexable, PropertyID: propertyID}
	}
	if len(def.Indexes) == 0 {
		return nil, &OpError{Kind: ErrPropertyNotIndexable, PropertyID: propertyID, Detail: "no index declared"}
	}
	return s.QueryByIndex(propertyID, def.Indexes[0].ID, def.Indexes[0].KeyFn(v))
}

// QueryAbsent 查询属性处于"不存在"标记的实例。
func (s *Store) QueryAbsent(propertyID string) ([]string, error) {
	return s.QueryByValue(propertyID, Absent)
}

// Get 读取实例当前属性值。
func (s *Store) Get(instanceID, propertyID string) (Value, error) {
	inst := s.getInstance(instanceID)
	if inst == nil {
		return Absent, &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID, PropertyID: propertyID}
	}
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	if inst.deleted {
		return Absent, &OpError{Kind: ErrInstanceNotFound, InstanceID: instanceID, PropertyID: propertyID}
	}
	return inst.getProp(propertyID), nil
}

// RecoveryDecision 记录恢复过程对单个未完成事务的判定。
type RecoveryDecision struct {
	TxnID    uint64
	Kind     string
	Decision string
	Reason   string
}

// Recover 崩溃后恢复：判定每个未完成事务并回滚或前滚（骨架）。
// 只扫描 WAL 中未完成的事务记录，开销不随索引条目总数增长。
func (s *Store) Recover() []RecoveryDecision {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	pending := s.wal.Pending()
	decisions := make([]RecoveryDecision, 0, len(pending))
	for _, rec := range pending {
		var d RecoveryDecision
		d.TxnID = rec.TxnID
		d.Kind = rec.Kind
		if rec.State == TxnCommitted {
			s.rollForward(rec)
			d.Decision = "rolled-forward"
			d.Reason = "commit record found in WAL"
			if rec.Clock > s.clock.Load() {
				s.clock.Store(rec.Clock)
			}
		} else {
			s.rollBack(rec)
			d.Decision = "rolled-back"
			d.Reason = "no commit record in WAL"
		}
		s.wal.End(rec.TxnID)
		s.log.Logf("RECOVER txn=%d kind=%s decision=%s reason=%q",
			rec.TxnID, rec.Kind, d.Decision, d.Reason)
		decisions = append(decisions, d)
	}
	return decisions
}

// rollForward 幂等重放已提交事务的全部条目。
func (s *Store) rollForward(rec *TxnRecord) {
	for i := range rec.Undo {
		e := &rec.Undo[i]
		def := s.propDef(e.PropertyID)
		if def == nil {
			continue
		}
		if rec.Kind == "delete" {
			s.mu.Lock()
			delete(s.instances, e.InstanceID)
			s.mu.Unlock()
			forwardEntry(nil, def, e, s.log)
			continue
		}
		inst := s.getInstance(e.InstanceID)
		if inst == nil {
			continue
		}
		inst.mu.Lock()
		forwardEntry(inst, def, e, s.log)
		inst.mu.Unlock()
	}
}

// rollBack 把未提交事务的全部条目恢复到写入前状态（属性值与索引条目一起回退）。
func (s *Store) rollBack(rec *TxnRecord) {
	for i := range rec.Undo {
		e := &rec.Undo[i]
		def := s.propDef(e.PropertyID)
		if def == nil {
			continue
		}
		if rec.Kind == "delete" {
			inst := s.getInstance(e.InstanceID)
			if inst == nil {
				inst = &instance{}
				s.mu.Lock()
				s.instances[e.InstanceID] = inst
				s.mu.Unlock()
			}
			inst.mu.Lock()
			inst.deleted = false
			revertEntry(inst, def, e, s.log)
			inst.mu.Unlock()
			continue
		}
		inst := s.getInstance(e.InstanceID)
		if inst == nil {
			continue
		}
		inst.mu.Lock()
		revertEntry(inst, def, e, s.log)
		inst.mu.Unlock()
	}
}

func sortedIDs(ids []string) []string {
	sort.Strings(ids)
	return ids
}
