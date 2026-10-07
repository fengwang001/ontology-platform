package batchimport

import (
	"fmt"
	"sort"
	"strings"
)

// 本文件提供测试夹具：
//   - newTestRegistry：注册 Widget(amount int64, label string) / Counter 两个类型；
//   - 一组行为确定性、依赖批内可见性的前置/后置钩子（求和、唯一标签、最终上限）；
//   - naiveRun：独立维护全量状态、严格按列表顺序逐条应用并同步触发钩子的朴素模型。
//
// 朴素模型刻意用最简单的 map 复制方式维护状态，与生产实现的覆盖层方案
// 没有任何共享代码路径，从而构成有意义的交叉对照。

const (
	typeWidget  = "Widget"
	typeCounter = "Counter"
)

// hookSpec 控制测试类型注册哪些钩子。
type hookSpec struct {
	sumPre    bool // 前置钩子：当前 amount 必须等于「可见记录累计和」
	uniquePre bool // 前置钩子：label 在可见范围内必须唯一
	capPost   bool // 后置钩子：最终累计 amount 不得超过 postCap
	postCap   int64
}

func registerTestTypes(r *Registry, spec hookSpec) {
	t := &ObjectType{
		Name:       typeWidget,
		FieldTypes: map[string]string{"amount": "int64", "label": "string"},
	}
	if spec.sumPre {
		t.PreHooks = append(t.PreHooks, sumPreHook)
	}
	if spec.uniquePre {
		t.PreHooks = append(t.PreHooks, uniqueLabelPreHook)
	}
	if spec.capPost {
		cap := spec.postCap
		t.PostHooks = append(t.PostHooks, func(ctx *PostHookContext) error {
			total := int64(0)
			for _, inst := range ctx.reg.snapshotTypeLocked(typeWidget) {
				total += inst.Fields["amount"].(int64)
			}
			for _, e := range ctx.st.overlay[typeWidget] {
				total += e.inst.Fields["amount"].(int64)
			}
			if total > cap {
				return postHookFailure(fmt.Sprintf("batch total %d exceeds cap %d", total, cap))
			}
			return nil
		})
	}
	r.RegisterObjectType(t)
	r.RegisterObjectType(&ObjectType{Name: typeCounter, FieldTypes: map[string]string{"n": "int64"}})
}

// sumPreHook 依赖按列表顺序确定性解析的派生值：
// 每条记录的 amount 必须严格等于「之前可见记录累计和 + 1」，
// 这样一旦可见范围错位（看到了未来记录 / 看不到之前记录），判定立即不同。
func sumPreHook(ctx *HookContext, rec Record) error {
	sum, _ := ctx.Scratch()["widgetSum"].(int64)
	want := sum + 1
	got := rec.Fields["amount"].(int64)
	if got != want {
		return preHookFailure(ctx.Index(),
			fmt.Sprintf("amount=%d but expected %d from visible prefix sum", got, want))
	}
	ctx.Scratch()["widgetSum"] = sum + got
	return nil
}

// uniqueLabelPreHook 每次实际扫描可见 Widget 主键（通过 Get 探测），
// 标签重复即拒绝；用于直接观察可见性解析的步数开销。
func uniqueLabelPreHook(ctx *HookContext, rec Record) error {
	label := rec.Fields["label"].(string)
	ids := ctx.VisibleIDs(typeWidget)
	for _, id := range ids {
		inst, ok := ctx.Get(typeWidget, id)
		if !ok {
			continue
		}
		if inst.Fields["label"] == label {
			return preHookFailure(ctx.Index(), "duplicate visible label: "+label)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 朴素参考模型
// ---------------------------------------------------------------------------

// naiveModel 独立维护全量状态，严格按列表顺序逐条应用、同步触发钩子。
type naiveModel struct {
	types map[string]map[string]string
	data  map[string]map[string]Instance
	pre   map[string][]PreHook
	post  map[string][]PostHook
	spec  hookSpec
}

func newNaiveModel(seed *Registry, spec hookSpec) *naiveModel {
	m := &naiveModel{
		types: map[string]map[string]string{},
		data:  map[string]map[string]Instance{},
		pre:   map[string][]PreHook{},
		post:  map[string][]PostHook{},
		spec:  spec,
	}
	seed.mu.Lock()
	defer seed.mu.Unlock()
	for name, t := range seed.types {
		m.types[name] = map[string]string{}
		for f, ty := range t.FieldTypes {
			m.types[name][f] = ty
		}
		m.pre[name] = append(m.pre[name], t.PreHooks...)
		m.post[name] = append(m.post[name], t.PostHooks...)
	}
	for name, insts := range seed.data {
		m.data[name] = map[string]Instance{}
		for id, inst := range insts {
			m.data[name][id] = inst.clone()
		}
	}
	return m
}

// naiveResult 是朴素模型的逐条结果。
type naiveResult struct {
	OK     bool
	Reason string
	Kind   Kind
}

// naiveReport 与生产 Report 对齐的朴素结论。
type naiveReport struct {
	committed bool
	errKind   Kind
	errIndex  int
	records   []naiveResult
}

// run 完全按规格文字逐行实现，不追求性能（每次钩子视图即全量 map）。
func (m *naiveModel) run(records []Record, semantics Semantics) *naiveReport {
	res := make([]naiveResult, len(records))
	for i := range res {
		res[i] = naiveResult{}
	}

	validate := func(rec Record, i int) error {
		fields, ok := m.types[rec.Type]
		if !ok {
			return invalidParam(i, "unknown object type: "+rec.Type)
		}
		if rec.ID == "" {
			return invalidParam(i, "empty primary key for type "+rec.Type)
		}
		for f, v := range rec.Fields {
			want, ok := fields[f]
			if !ok {
				return invalidParam(i, "unknown field "+rec.Type+"."+f)
			}
			if v == nil || goTypeName(v) != want {
				return invalidParam(i, fmt.Sprintf("field %s.%s type mismatch", rec.Type, f))
			}
		}
		return nil
	}

	// 字段类型校验与列表顺序无关，先全部做完；
	// 主键重复检测的口径与生产实现保持一致（见顺序循环中的 occupied）。
	perr := make([]error, len(records))
	var firstParamErr error
	for i, rec := range records {
		perr[i] = validate(rec, i)
		if perr[i] != nil && firstParamErr == nil {
			firstParamErr = perr[i]
		}
	}

	if semantics == AllOrNothing {
		// 全有或全无语义：字段合法的记录中不得出现同主键重复；
		// 任意参数非法（含重复）都在任何钩子触发前拒绝整批。
		seen := map[string]struct{}{}
		for i, rec := range records {
			key := rec.Type + "\x00" + rec.ID
			if _, dup := seen[key]; dup && perr[i] == nil {
				perr[i] = invalidParam(i, "duplicate primary key: "+rec.Type+"/"+rec.ID)
			}
			seen[key] = struct{}{}
		}
		firstParamErr = nil
		for _, e := range perr { // 下标升序的第一个参数错误即报告对象
			if e != nil {
				firstParamErr = e
				break
			}
		}
	}
	if semantics == BestEffort {
		// 尽力而为语义：重复是输入列表的静态非法属性，对全部记录无差别检测
		//（与生产 prepare 阶段一致）；字段错误优先于重复归属到本记录。
		seen := map[string]struct{}{}
		for i, rec := range records {
			key := rec.Type + "\x00" + rec.ID
			if _, dup := seen[key]; dup && perr[i] == nil {
				perr[i] = invalidParam(i, "duplicate primary key: "+rec.Type+"/"+rec.ID)
			}
			seen[key] = struct{}{}
		}
	}

	if semantics == AllOrNothing && firstParamErr != nil {
		for i, e := range perr {
			if e != nil {
				res[i].Reason = e.Error()
				res[i].Kind = KindInvalidParam
			}
		}
		e, _ := AsImportError(firstParamErr)
		return &naiveReport{committed: false, errKind: e.Kind, errIndex: e.Index, records: res}
	}

	// working 是朴素模型的「已应用前缀」状态；批次失败时整个丢弃，
	// 天然等价于全量撤销。
	working := cloneAll(m.data)
	scratch := map[string]any{}

	// 朴素地逐条跑钩子：钩子读到的「可见状态」就是 working（前缀状态）。
	// 为直接复用生产钩子函数，把 working 装进一个临时 Registry，
	// 并让朴素 staging 始终为空（可见状态全部由 working 表达）。
	viewReg := &Registry{types: map[string]*ObjectType{}, data: working}
	for name := range m.types {
		viewReg.types[name] = &ObjectType{Name: name, FieldTypes: m.types[name]}
	}
	st := newStaging()

	for i, rec := range records {
		if semantics == BestEffort && perr[i] != nil {
			res[i].Reason = perr[i].Error()
			res[i].Kind = KindInvalidParam
			continue
		}
		workingRef := working
		ctx := &HookContext{
			index:   i,
			reg:     viewReg,
			st:      st,
			scratch: scratch,
			visibleIDs: func(typeName string) []string {
				ids := make([]string, 0, len(workingRef[typeName]))
				for id := range workingRef[typeName] {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				return ids
			},
		}
		var err error
		for _, h := range m.pre[rec.Type] {
			if err = h(ctx, rec); err != nil {
				break
			}
		}
		if err != nil {
			e, _ := AsImportError(normalize(i, KindPreHook, err))
			res[i].Reason = e.Error()
			res[i].Kind = KindPreHook
			if semantics == AllOrNothing {
				return &naiveReport{committed: false, errKind: e.Kind, errIndex: i, records: res}
			}
			continue
		}
		// 朴素应用：直接写入 working（staging 保持为空，视图穿透到 working）。
		if working[rec.Type] == nil {
			working[rec.Type] = map[string]Instance{}
		}
		working[rec.Type][rec.ID] = Instance{
			Type: rec.Type, ID: rec.ID, Fields: cloneFields(rec.Fields),
		}
		res[i].OK = true
	}

	if semantics == AllOrNothing {
		// 后置钩子看到最终状态：working 即整批应用后状态，staging 为空。
		postCtx := &PostHookContext{reg: viewReg, st: st, scratch: scratch}
		names := make([]string, 0)
		for name := range m.post {
			if len(m.post[name]) > 0 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			for _, h := range m.post[name] {
				if err := h(postCtx); err != nil {
					e, _ := AsImportError(normalize(-1, KindPostHook, err))
					return &naiveReport{committed: false, errKind: e.Kind, errIndex: -1, records: res}
				}
			}
		}
		m.data = working
		return &naiveReport{committed: true, records: res}
	}

	m.data = working
	return &naiveReport{committed: true, records: res}
}

func cloneAll(in map[string]map[string]Instance) map[string]map[string]Instance {
	out := make(map[string]map[string]Instance, len(in))
	for k, m := range in {
		out[k] = make(map[string]Instance, len(m))
		for id, inst := range m {
			out[k][id] = inst.clone()
		}
	}
	return out
}

func goTypeName(v any) string {
	return fmt.Sprintf("%T", v)
}

func formatRecords(records []Record) string {
	var b strings.Builder
	for i, rec := range records {
		fmt.Fprintf(&b, "  [%d] %s/%s %v\n", i, rec.Type, rec.ID, rec.Fields)
	}
	return b.String()
}

func formatReport(rep *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "committed=%v", rep.Committed)
	if rep.Err != nil {
		fmt.Fprintf(&b, " err=%v", rep.Err)
	}
	b.WriteString("\n")
	for _, rr := range rep.Records {
		if rr.OK {
			fmt.Fprintf(&b, "  [%d] OK\n", rr.Index)
		} else {
			fmt.Fprintf(&b, "  [%d] FAIL %s\n", rr.Index, rr.Reason)
		}
	}
	return b.String()
}
