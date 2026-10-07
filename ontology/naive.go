package ontology

// NaiveModel 是与实现完全独立的参照模型：
// 它不使用快照、overlay、并发、版本重放等任何机制，
// 只用一个 map 维护「全量已存在状态」，
// 严格按输入列表顺序逐条应用，并在每一步同步触发同一批钩子。
//
// 它是需求语义的「字面直译」，供随机差分测试作为判定基准。
type NaiveModel struct {
	reg   *Registry
	state map[string]map[string]Instance
	// agg 维护每个类型每个聚合的当前值，与 state 同步更新。
	agg map[string]map[string]float64
}

// NewNaiveModel 创建以给定存储为初始全量状态的朴素模型。
func NewNaiveModel(reg *Registry, st *Store) *NaiveModel {
	m := &NaiveModel{
		reg:   reg,
		state: map[string]map[string]Instance{},
		agg:   map[string]map[string]float64{},
	}
	for name := range reg.types {
		m.state[name] = map[string]Instance{}
		for pk, inst := range st.CurrentInstances(name) {
			m.state[name][pk] = inst
		}
		m.rebuildAgg(name)
	}
	return m
}

// State 返回某类型当前全量状态的拷贝（测试断言最终状态用）。
func (m *NaiveModel) State(typeName string) map[string]Instance {
	src := m.state[typeName]
	out := make(map[string]Instance, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (m *NaiveModel) rebuildAgg(typeName string) {
	ot, ok := m.reg.Lookup(typeName)
	if !ok {
		return
	}
	values := map[string]float64{}
	for _, d := range ot.Aggregates {
		switch d.Method {
		case AggCount:
			values[d.Name] = float64(len(m.state[typeName]))
		case AggSum:
			var sum float64
			for _, inst := range m.state[typeName] {
				sum += numericValue(inst[d.Field])
			}
			values[d.Name] = sum
		}
	}
	m.agg[typeName] = values
}

// naiveView 直接读朴素模型全量状态的视图。
type naiveView struct {
	m        *NaiveModel
	typeName string
}

func (v *naiveView) Get(pk string) (Instance, bool) {
	inst, ok := v.m.state[v.typeName][pk]
	return inst, ok
}

func (v *naiveView) Exists(pk string) bool {
	_, ok := v.Get(pk)
	return ok
}

func (v *naiveView) Field(pk, field string) (Value, bool) {
	inst, ok := v.Get(pk)
	if !ok {
		return nil, false
	}
	val, ok := inst[field]
	return val, ok
}

func (v *naiveView) Aggregate(name string) (float64, bool) {
	val, ok := v.m.agg[v.typeName][name]
	return val, ok
}

func (v *naiveView) Count() float64 {
	for _, d := range v.m.reg.types[v.typeName].Aggregates {
		if d.Method == AggCount {
			return v.m.agg[v.typeName][d.Name]
		}
	}
	return 0
}

// NaiveResult 与 BatchResult 对应，但由朴素模型逐条产生。
type NaiveResult struct {
	Type      string
	Semantic  Semantics
	Records   []RecordResult
	PostError *HookError
	Committed bool
}

// FirstError 与实现保持同样的归一化报告次序。
func (r *NaiveResult) FirstError() *HookError {
	var firstParam *HookError
	var firstPre *HookError
	for i := range r.Records {
		e := r.Records[i].Err
		if e == nil {
			continue
		}
		if e.Kind == KindParamInvalid && firstParam == nil {
			firstParam = e
		}
		if e.Kind == KindPreHook && firstPre == nil {
			firstPre = e
		}
	}
	if firstParam != nil {
		return firstParam
	}
	if firstPre != nil {
		return firstPre
	}
	return r.PostError
}

// Run 在朴素模型上执行一次导入，语义与 Importer 完全一致。
func (m *NaiveModel) Run(b Batch) *NaiveResult {
	ot, ok := m.reg.Lookup(b.Type)
	n := len(b.Records)
	results := make([]RecordResult, n)
	fail := func(he *HookError) *NaiveResult {
		for j := range results {
			results[j] = RecordResult{Index: j, PK: b.Records[j].PK,
				Status: StatusFailed, Err: he}
		}
		return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
			Records: results, Committed: false}
	}
	if !ok {
		return fail(postError("unknown_type", "object type not registered"))
	}

	// 阶段 0：主键重复（整批参数非法，最先）。
	seen := map[string]int{}
	for i, rec := range b.Records {
		if first, dup := seen[rec.PK]; dup {
			return fail(paramError(i, "duplicate_pk",
				"primary key repeated within batch (first at index "+itoa(first)+")"))
		}
		seen[rec.PK] = i
	}

	// 全有全无时：参数非法（字段类型不符）整体优先于前置钩子。
	if b.Semantic == SemAllOrNothing {
		for i, rec := range b.Records {
			if he := ot.validateSchema(rec); he != nil {
				e := *he
				e.Index = i
				ee := &e
				for j := range b.Records {
					results[j] = RecordResult{Index: j, PK: b.Records[j].PK,
						Status: StatusFailed, Err: ee}
				}
				return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
					Records: results, Committed: false}
			}
		}
	}

	stm := m.state[b.Type]
	type applied struct {
		pk       string
		existed  bool
		previous Instance
	}
	var journal []applied
	rollback := func() {
		for i := len(journal) - 1; i >= 0; i-- {
			u := journal[i]
			if u.existed {
				stm[u.pk] = u.previous
			} else {
				delete(stm, u.pk)
			}
		}
		journal = nil
		m.rebuildAgg(b.Type)
	}

	for i, rec := range b.Records {
		results[i] = RecordResult{Index: i, PK: rec.PK, Status: StatusFailed}
		if he := ot.validateSchema(rec); he != nil {
			e := *he
			e.Index = i
			results[i].Err = &e
			continue
		}
		view := &naiveView{m: m, typeName: b.Type}
		if he := ot.Pre(&HookContext{TypeName: b.Type, Index: i}, rec, view); he != nil {
			e := *he
			e.Kind = KindPreHook
			e.Index = i
			results[i].Err = &e
			if b.Semantic == SemAllOrNothing {
				for j := i + 1; j < n; j++ {
					results[j] = RecordResult{Index: j, PK: b.Records[j].PK,
						Status: StatusSkippedUnprocessed}
				}
				rollback() // 前置失败：逆序撤销已应用前缀并重建聚合
				return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
					Records: markRolledBackNaive(results), Committed: false}
			}
			continue
		}

		prev, existed := stm[rec.PK]
		var prevCopy Instance
		if existed {
			prevCopy = Instance{}
			for k, v := range prev {
				prevCopy[k] = v
			}
		}
		journal = append(journal, applied{pk: rec.PK, existed: existed, previous: prevCopy})
		merged := Instance{}
		for k, v := range prev {
			merged[k] = v
		}
		for k, v := range rec.Fields {
			merged[k] = v
		}
		stm[rec.PK] = merged
		m.rebuildAgg(b.Type) // 朴素模型故意全量重算，保持简单
		results[i].Status = StatusApplied
	}

	if b.Semantic == SemBestEffort {
		return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
			Records: results, Committed: true}
	}

	// 全有全无：若有失败先回滚。
	for i := range results {
		if results[i].Status == StatusFailed {
			rollback()
			return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
				Records: markRolledBackNaive(results), Committed: false}
		}
	}
	// 后置钩子看到最终状态。
	if ot.Post != nil {
		view := &naiveView{m: m, typeName: b.Type}
		if he := ot.Post(&HookContext{TypeName: b.Type, Index: -1}, view); he != nil {
			e := *he
			e.Kind = KindPostHook
			e.Index = -1
			rollback()
			return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
				Records: markRolledBackNaive(results), PostError: &e, Committed: false}
		}
	}
	return &NaiveResult{Type: b.Type, Semantic: b.Semantic,
		Records: results, Committed: true}
}

func markRolledBackNaive(results []RecordResult) []RecordResult {
	for i := range results {
		if results[i].Status == StatusApplied {
			results[i].Status = StatusFailed
			results[i].Err = &HookError{Kind: KindPostHook, Index: -1,
				Code:    "batch_rolled_back",
				Message: "record reverted because the batch did not commit"}
		}
	}
	return results
}
