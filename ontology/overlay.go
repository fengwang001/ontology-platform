package ontology

// AccessCounters 记录可见性解析的底层访问量，
// 作为「单次解析开销不随批次总长度增长」的可验证证据。
//   - Probes：哈希探测次数（Get/Exists/Aggregate 等）
//   - PrefixScans：遍历批内已应用记录的次数（受控实现恒为 0）
type AccessCounters struct {
	Probes      int
	PrefixScans int
}

// undoEntry 记录一次 apply 之前该主键的状态，供逆序撤销。
type undoEntry struct {
	pk       string
	existed  bool
	previous Instance
}

// overlay 承载一个批次对单个对象类型的「尚未提交前缀效果」。
// 不变量：
//   - pending 恰好包含截至当前、已通过前置钩子并应用的记录；
//   - 任何查询都是哈希探测，绝不遍历 pending（PrefixScans 恒 0）；
//   - aggValue 随 apply 增量维护，读取恒 O(1)。
type overlay struct {
	typeName string
	snap     *Snapshot

	// pending 为已应用前缀的主键 -> 当前实例（只读替换，不原地改）。
	pending map[string]Instance
	// undo 按应用顺序记录，UndoAll 逆序回放。
	undo []undoEntry

	aggDef   map[string]AggregateDef
	aggValue map[string]float64
}

func newOverlay(typeName string, snap *Snapshot, defs []AggregateDef) *overlay {
	ov := &overlay{
		typeName: typeName,
		snap:     snap,
		pending:  map[string]Instance{},
		aggDef:   map[string]AggregateDef{},
		aggValue: map[string]float64{},
	}
	for _, d := range defs {
		ov.aggDef[d.Name] = d
	}
	// 基线聚合在批次构建时一次性算好（成本属于批次初始化，
	// 与批次长度无关；且只与已提交存量有关）。
	ov.buildBaseAggregates()
	return ov
}

// buildBaseAggregates 基于快照一次性计算所有聚合的基线值。
func (ov *overlay) buildBaseAggregates() {
	ts := ov.snap.State(ov.typeName)
	for _, d := range ov.aggDef {
		switch d.Method {
		case AggCount:
			if ts != nil {
				ov.aggValue[d.Name] = float64(len(ts.Instances))
			} else {
				ov.aggValue[d.Name] = 0
			}
		case AggSum:
			var sum float64
			if ts != nil {
				for _, inst := range ts.Instances {
					sum += numericValue(inst[d.Field])
				}
			}
			ov.aggValue[d.Name] = sum
		}
	}
}

// view 为某条记录的钩子构造可见范围视图。
func (ov *overlay) view(c *AccessCounters) *ScopedView {
	return &ScopedView{ov: ov, c: c}
}

// apply 应用一条已通过前置钩子的记录（upsert：字段级合并），
// 并增量更新聚合值。应用本身 O(1)。
func (ov *overlay) apply(rec Record) {
	var previous Instance
	existed := false
	if cur, ok := ov.pending[rec.PK]; ok {
		previous, existed = cloneInstance(cur), true
	} else if base, ok := ov.snap.Get(ov.typeName, rec.PK); ok {
		previous, existed = cloneInstance(base), true
	}

	merged := Instance{}
	for k, v := range previous {
		merged[k] = v
	}
	for k, v := range rec.Fields {
		merged[k] = v
	}
	ov.pending[rec.PK] = merged
	ov.undo = append(ov.undo, undoEntry{pk: rec.PK, existed: existed, previous: previous})

	for _, d := range ov.aggDef {
		switch d.Method {
		case AggCount:
			if !existed {
				ov.aggValue[d.Name]++
			}
		case AggSum:
			// upsert：只对新出现主键累加；更新已存在主键时
			// 差值由字段覆盖产生，按新旧字段值之差修正。
			if !existed {
				ov.aggValue[d.Name] += numericValue(rec.Fields[d.Field])
			} else {
				oldV := numericValue(previous[d.Field])
				newV := numericValue(merged[d.Field])
				ov.aggValue[d.Name] += newV - oldV
			}
		}
	}
}

// UndoAll 逆序撤销全部已应用记录，撤销后 overlay 与刚构建时一致，
// 配合快照保证系统状态回到批次开始前。
func (ov *overlay) UndoAll() {
	for i := len(ov.undo) - 1; i >= 0; i-- {
		u := ov.undo[i]
		if u.existed {
			ov.pending[u.pk] = u.previous
		} else {
			delete(ov.pending, u.pk)
		}
	}
	ov.undo = ov.undo[:0]
	// 聚合为增量维护；快照基线在批次内不变，
	// 整批撤销后直接恢复到基线即等价于「零前缀」状态。
	ov.buildBaseAggregates()
	// 撤销完成后 pending 不应再含本批任何新建主键。
}

// finalChanged 生成提交所需的最终写入映射（前缀全集）。
func (ov *overlay) finalChanged() map[string]Instance {
	out := make(map[string]Instance, len(ov.pending))
	for pk, inst := range ov.pending {
		out[pk] = inst
	}
	return out
}

func numericValue(v Value) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

func cloneInstance(inst Instance) Instance {
	if inst == nil {
		return nil
	}
	out := make(Instance, len(inst))
	for k, v := range inst {
		out[k] = v
	}
	return out
}

// ScopedView 是前置/后置钩子查询可见状态的唯一入口。
// 查询顺序固定为「批内已应用前缀」优先、快照其次，
// 因而既不是批次开始前静态快照（能看到前缀效果），
// 也不是批次结束后最终状态（看不到尚未处理的记录）。
type ScopedView struct {
	ov *overlay
	c  *AccessCounters
}

func (v *ScopedView) probe() {
	if v.c != nil {
		v.c.Probes++
	}
}

// Get 返回某主键在当前可见范围内的实例。
// 开销：至多两次哈希探测，与批次总长度无关。
func (v *ScopedView) Get(pk string) (Instance, bool) {
	v.probe()
	if inst, ok := v.ov.pending[pk]; ok {
		return inst, true
	}
	v.probe()
	return v.ov.snap.Get(v.ov.typeName, pk)
}

// Exists 判断主键在当前可见范围内是否存在。开销 O(1)。
func (v *ScopedView) Exists(pk string) bool {
	_, ok := v.Get(pk)
	return ok
}

// Field 读取某主键某字段的当前可见值。开销 O(1)。
func (v *ScopedView) Field(pk, field string) (Value, bool) {
	inst, ok := v.Get(pk)
	if !ok {
		return nil, false
	}
	val, ok := inst[field]
	return val, ok
}

// Aggregate 读取当前前缀下的派生聚合值。开销 O(1)。
func (v *ScopedView) Aggregate(name string) (float64, bool) {
	v.probe()
	val, ok := v.ov.aggValue[name]
	return val, ok
}

// Count / Sum 为常用聚合的便捷封装。
func (v *ScopedView) Count() float64 {
	for _, d := range v.ov.aggDef {
		if d.Method == AggCount {
			val, _ := v.Aggregate(d.Name)
			return val
		}
	}
	return 0
}
