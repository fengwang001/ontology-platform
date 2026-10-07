package ontology

import "sync"

// Kernel 是三个模块的协作编排者：
//   - store（实例版本仲裁）
//   - aggregateIndex（聚合视图增量维护）
//   - errors.go（一致性校验与错误归一化）
//
// 单一全局提交互斥锁 (commitMu) 串行化全部写入/删除，使它们对所有受影响
// 聚合视图的效果严格等价于按某个全局串行顺序逐一应用，且重放同一序列得到
// 完全相同的结果。聚合状态修改与实例版本落盘在同一临界区内完成，因此一次
// 提交对多个聚合视图的更新在提交的生效时刻整体可见，不引入额外生效时延。
type Kernel struct {
	// commitMu 是提交与一致性读取共用的栅栏：提交取写锁，使「实例落盘 +
	// 全部聚合 delta 生效」对任何读路径都是一个不可分割的线性化点，杜绝
	// 「聚合已反映而源实例仍旧」（或反之）的跨存储中间态。
	commitMu sync.RWMutex
	store    *store
	agg      *aggregateIndex
	sn       int64 // 全局提交序号：即每次提交的「生效时刻」
}

// NewKernel 创建一个空子系统。
func NewKernel() *Kernel {
	return &Kernel{store: newStore(), agg: newAggregateIndex()}
}

// RegisterType 登记对象类型。
func (k *Kernel) RegisterType(schema ObjectTypeSchema) {
	k.store.registerType(schema)
}

// RegisterView 登记聚合视图，并校验视图定义的字段合法性（仅在装配期）。
func (k *Kernel) RegisterView(def AggregateDef) error {
	ot, ok := k.store.objectTypeByName(def.SourceType)
	if !ok {
		return invalidArgumentf("unknown source type %q", def.SourceType)
	}
	if def.Name == "" {
		return invalidArgumentf("aggregate view name is empty")
	}
	gt, ok := ot.schema.Attrs[def.GroupBy]
	if !ok || gt != TypeString {
		return invalidArgumentf("group-by field %q must be a declared string attribute", def.GroupBy)
	}
	vt, ok := ot.schema.Attrs[def.ValueField]
	if !ok || (vt != TypeInt && vt != TypeDouble) {
		return invalidArgumentf("value field %q must be a declared numeric attribute", def.ValueField)
	}
	k.store.registerView(def)
	return nil
}

// CommitSN 返回最近一次成功提交的全局生效序号（0 表示尚无提交）。
func (k *Kernel) CommitSN() int64 {
	k.commitMu.RLock()
	defer k.commitMu.RUnlock()
	return k.sn
}

// WriteResult 是写入成功回执，同时携带本次操作触碰数据规模的证明信息。
type WriteResult = CommitResult

// Write 执行一次乐观并发写入。拒绝次序（只报第一个命中的原因）：
//  1. 参数非法：类型/主键为空、分组键为空、属性值缺失或类型不符；
//  2. 乐观并发凭证不匹配；
//  3. 对「不存在」的语义操作（写入允许在凭证正确时复活已删除实例）。
//
// 被拒绝的写入在任何状态变更之前返回，不占用版本号，不触碰聚合。
func (k *Kernel) Write(w Write) (WriteResult, error) {
	ot, err := k.validateWrite(w)
	if err != nil {
		return CommitResult{}, err
	}

	k.commitMu.Lock()
	defer k.commitMu.Unlock()

	meter := &CostMeter{}
	old, existed := k.store.get(w.Type, w.Key, meter)

	// 凭证仲裁。store 的点查在提交锁内，因此凭证判定与后续落盘之间不存在窗口。
	var currentVersion int64
	if existed {
		currentVersion = old.Version
	}
	if w.Prev != currentVersion {
		return CommitResult{}, versionConflictf(
			"optimistic credential mismatch for %q: prev=%d current=%d", w.Key, w.Prev, currentVersion)
	}

	newVersion := currentVersion + 1 // 只有走到这里才分配新版本号
	k.sn++
	rec := &Record{
		Key:      w.Key,
		Version:  newVersion,
		Deleted:  false,
		Attrs:    cloneAttrs(w.Attrs),
		CommitSN: k.sn,
	}

	deltas := k.buildDeltas(ot, defsOn(k.store, w.Type), old, existed, rec)
	// 顺序无关紧要——二者在同一临界区内，对查询者是同一个不可分割事件。
	k.agg.apply(deltas, meter)
	k.store.put(w.Type, rec)

	return CommitResult{Key: w.Key, Version: newVersion, CommitSN: k.sn}, nil
}

// Delete 删除一个实例（从所有聚合分组移除）。拒绝次序同 Write：
// 参数非法 -> 凭证不匹配 -> 从未成功提交过任何版本（ErrNotFound）。
// 「存在但已删除后再次删除」与「从未提交过」是两类不同错误：前者为
// ErrAlreadyDeleted（且需凭证匹配），后者为 ErrNotFound。
func (k *Kernel) Delete(d Delete) (CommitResult, error) {
	if d.Type == "" {
		return CommitResult{}, invalidArgumentf("object type is empty")
	}
	ot, ok := k.store.objectTypeByName(d.Type)
	if !ok {
		return CommitResult{}, invalidArgumentf("unknown object type %q", d.Type)
	}
	if d.Key == "" {
		return CommitResult{}, invalidArgumentf("primary key %q is empty", ot.schema.KeyField)
	}

	k.commitMu.Lock()
	defer k.commitMu.Unlock()

	meter := &CostMeter{}
	old, existed := k.store.get(d.Type, d.Key, meter)

	var currentVersion int64
	if existed {
		currentVersion = old.Version
	}
	if d.Prev != currentVersion {
		return CommitResult{}, versionConflictf(
			"optimistic credential mismatch for %q: prev=%d current=%d", d.Key, d.Prev, currentVersion)
	}

	// 凭证正确之后才区分「从未提交过」与「已删除后再次删除」。
	if !existed {
		return CommitResult{}, notFoundf("cannot delete %q: never committed any version", d.Key)
	}
	if old.Deleted {
		return CommitResult{}, alreadyDeletedf("cannot delete %q: instance is already deleted", d.Key)
	}

	newVersion := old.Version + 1
	k.sn++
	tomb := &Record{
		Key:      d.Key,
		Version:  newVersion,
		Deleted:  true,
		Attrs:    map[string]Value{},
		CommitSN: k.sn,
	}
	deltas := k.buildDeltas(ot, defsOn(k.store, d.Type), old, true, tomb)
	k.agg.apply(deltas, meter)
	k.store.put(d.Type, tomb)

	return CommitResult{Key: d.Key, Version: newVersion, Deleted: true, CommitSN: k.sn}, nil
}

// Query 查询某视图某分组的当前取值。只读取一个增量分组单元，不扫描任何
// 源实例记录：开销为 O(1)（只与「该分组是否存在」相关，与类型下实例总数
// 无关）。返回的 CostMeter 使该性质可被测试直接断言。
func (k *Kernel) Query(view, group string) (GroupValue, *CostMeter, error) {
	k.commitMu.RLock()
	defer k.commitMu.RUnlock()
	if _, ok := k.store.viewOf[view]; !ok {
		return GroupValue{}, nil, invalidArgumentf("unknown aggregate view %q", view)
	}
	if group == "" {
		return GroupValue{}, nil, invalidArgumentf("group key is empty")
	}
	meter := &CostMeter{}
	val := k.agg.get(view, group, meter)
	return val, meter, nil
}

// GetInstance 点查一个实例的当前可见版本（供测试/外部一致性校验使用）。
func (k *Kernel) GetInstance(typeName, key string) (Record, bool) {
	k.commitMu.RLock()
	defer k.commitMu.RUnlock()
	rec, ok := k.store.get(typeName, key, nil)
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

// RecomputeView 用当时全部源实例最新可见版本全量重算一个视图（朴素口径），
// 供一致性校验：任何时刻它都必须与增量索引给出的查询结果逐项相等。
func (k *Kernel) RecomputeView(view string) map[string]GroupValue {
	k.commitMu.RLock()
	defer k.commitMu.RUnlock()
	def, ok := k.store.viewOf[view]
	if !ok {
		return nil
	}
	return k.agg.rebuild(def, k.store.scanType(def.SourceType))
}

// VerifyViewConsistency 在同一个读临界区内对增量索引与「源实例全量重算」做
// 逐项比对，返回不一致的首个分组（空字符串表示完全一致）。它以可重入执行的
// 方式断言不变量 I1：即使在并发提交风暴中，一次一致性观测内
// 「聚合 == 当时源实例最新可见版本的重算结果」也必须成立。
func (k *Kernel) VerifyViewConsistency(view string) (mismatchGroup string, query, recompute GroupValue) {
	k.commitMu.RLock()
	defer k.commitMu.RUnlock()
	def, ok := k.store.viewOf[view]
	if !ok {
		return "", GroupValue{}, GroupValue{}
	}
	full := k.agg.rebuild(def, k.store.scanType(def.SourceType))
	seen := map[string]bool{}
	for g, want := range full {
		seen[g] = true
		got := k.agg.get(view, g, nil)
		if got.Sum != want.Sum || got.Members != want.Members {
			return g, got, want
		}
	}
	// 索引中不应存在重算里没有的非空分组。
	for _, got := range k.agg.snapshotView(view) {
		if !seen[got.Group] && (got.Sum != 0 || got.Members != 0) {
			return got.Group, got, GroupValue{View: view, Group: got.Group}
		}
	}
	return "", GroupValue{}, GroupValue{}
}

// validateWrite 执行写入的参数校验（错误次序第 1 类），在取任何锁之前完成，
// 保证被拒绝操作不产生任何副作用。
func (k *Kernel) validateWrite(w Write) (*objectType, *Error) {
	if w.Type == "" {
		return nil, invalidArgumentf("object type is empty")
	}
	ot, ok := k.store.objectTypeByName(w.Type)
	if !ok {
		return nil, invalidArgumentf("unknown object type %q", w.Type)
	}
	if w.Key == "" {
		return nil, invalidArgumentf("primary key %q is empty", ot.schema.KeyField)
	}
	if w.Attrs == nil {
		return nil, invalidArgumentf("write for %q carries no attributes", w.Key)
	}
	groupFields := map[string]bool{}
	for _, def := range k.store.views[w.Type] {
		groupFields[def.GroupBy] = true
	}
	// 全部已声明字段都必须出现且类型相符；任何视图引用的分组键为空都在此拒绝。
	for name, want := range ot.schema.Attrs {
		got, present := w.Attrs[name]
		if !present {
			return nil, invalidArgumentf("missing attribute %q for %q", name, w.Key)
		}
		if got.Type != want {
			return nil, invalidArgumentf(
				"attribute %q for %q has type %s, want %s", name, w.Key, got.Type, want)
		}
		if want == TypeString && groupFields[name] && got.Str == "" {
			return nil, invalidArgumentf("string attribute %q (group key) for %q is empty", name, w.Key)
		}
	}
	// 拒绝未声明的多余字段，避免类型不符的脏数据进入实例存储。
	for name := range w.Attrs {
		if _, ok := ot.schema.Attrs[name]; !ok {
			return nil, invalidArgumentf("undeclared attribute %q for %q", name, w.Key)
		}
	}
	return ot, nil
}

// buildDeltas 计算一次提交对实例所属全部聚合视图的贡献变化。一次写入可能
// 同时改变多个视图（不同 GroupBy），所有 delta 随后在同一个 apply 调用、
// 同一把提交锁内生效。
func (k *Kernel) buildDeltas(ot *objectType, defs []AggregateDef, old *Record, existed bool, cur *Record) []delta {
	deltas := make([]delta, 0, len(defs))
	oldLive := existed && !old.Deleted
	curLive := !cur.Deleted
	for _, def := range defs {
		d := delta{view: def, hadOld: oldLive, hasNew: curLive}
		if oldLive {
			d.oldGroup = old.Attrs[def.GroupBy].Str
			d.oldVal = old.Attrs[def.ValueField].Num
		}
		if curLive {
			d.newGroup = cur.Attrs[def.GroupBy].Str
			d.newVal = cur.Attrs[def.ValueField].Num
		}
		deltas = append(deltas, d)
	}
	return deltas
}

func defsOn(s *store, typeName string) []AggregateDef {
	return s.views[typeName]
}
