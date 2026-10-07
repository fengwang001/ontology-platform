package engine_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/engine"
	"ontology/hooks"
	"ontology/naive"
	"ontology/spec"
)

// concurrentScenario 描述一组并发提交的最外层动作。
type concurrentScenario struct {
	defs    []spec.ActionDef
	actions []string // actions[i] 是序号为 i 的最外层调用
	n       int
}

// buildConcurrentScenario 构造 n 个最外层动作，每个动作内部
// 嵌套调用一个辅助动作，写入两个专属 marker 并更新共享 ledger。
// 序号 0 固定为 bootstrap（创建共享 ledger），其余动作依赖它，
// 以此验证显式序号调度下「先提交者未必先执行」的串行化语义。
func buildConcurrentScenario(n int) concurrentScenario {
	sc := concurrentScenario{n: n}
	sc.defs = append(sc.defs, spec.ActionDef{Name: "bootstrap", Body: []spec.Op{
		wr("ledger", "main", spec.OpCreate, map[string]any{"last": "none"}),
	}})
	sc.actions = append(sc.actions, "bootstrap")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("outer-%d", i)
		helper := fmt.Sprintf("helper-%d", i)
		sc.defs = append(sc.defs,
			spec.ActionDef{Name: helper, Body: []spec.Op{
				wr("marker", fmt.Sprintf("m-%d-b", i), spec.OpCreate,
					map[string]any{"owner": name, "seq": i}),
			}},
			spec.ActionDef{Name: name, Body: []spec.Op{
				wr("marker", fmt.Sprintf("m-%d-a", i), spec.OpCreate,
					map[string]any{"owner": name, "seq": i}),
				call(helper),
				wr("ledger", "main", spec.OpUpdate,
					map[string]any{"last": name}),
			}},
		)
		sc.actions = append(sc.actions, name)
	}
	sc.n = len(sc.actions)
	return sc
}

// newConcurrentEngine 创建引擎并注册场景所需的类型、动作与钩子。
// markerViews 收集每次 marker 前置钩子看到的 marker ID 集合，
// 用于验证不会观察到其他事务的半成品写入。
func newConcurrentEngine(t *testing.T, sc concurrentScenario,
	markerViews *[]markerObservation) *engine.Engine {
	t.Helper()
	e := engine.New()
	e.RegisterType("marker", spec.Schema{Fields: map[string]spec.FieldType{
		"owner": spec.FieldString,
		"seq":   spec.FieldInt,
	}})
	e.RegisterType("ledger", spec.Schema{Fields: map[string]spec.FieldType{
		"last": spec.FieldString,
	}})
	for _, def := range sc.defs {
		mustRegister(t, e, def)
	}
	e.RegisterPreHook("marker", "capture-markers", func(ctx hooks.Context) error {
		*markerViews = append(*markerViews, markerObservation{
			writeID: ctx.Write.ID,
			present: ctx.State.List("marker"),
		})
		return nil
	})
	return e
}

// markerObservation 记录一次 marker 前置钩子观察：
// 正在写入的实例 ID 与当时已存在的 marker 集合。
type markerObservation struct {
	writeID string
	present []string
}

func newConcurrentNaive(sc concurrentScenario) *naive.Model {
	m := naive.New()
	m.RegisterType("marker", spec.Schema{Fields: map[string]spec.FieldType{
		"owner": spec.FieldString,
		"seq":   spec.FieldInt,
	}})
	m.RegisterType("ledger", spec.Schema{Fields: map[string]spec.FieldType{
		"last": spec.FieldString,
	}})
	for _, def := range sc.defs {
		if err := m.RegisterAction(def); err != nil {
			panic(err)
		}
	}
	m.RegisterPreHook("marker", "capture-markers", func(ctx hooks.Context) error {
		return nil
	})
	return m
}

// runConcurrentBatch 以打乱的 goroutine 启动顺序提交全部最外层动作
// （含序号 0 的 bootstrap），每个调用携带显式序号，
// 引擎必须按序号串行执行。
func runConcurrentBatch(t *testing.T, e *engine.Engine, sc concurrentScenario,
	shuffleSeed int64) []error {
	t.Helper()
	errs := make([]error, sc.n)
	order := rand.New(rand.NewSource(shuffleSeed)).Perm(sc.n)
	var wg sync.WaitGroup
	for _, idx := range order {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			errs[seq] = e.ExecuteWithSeq(uint64(seq), sc.actions[seq])
		}(idx)
	}
	wg.Wait()
	return errs
}

// snapshotAll 导出类型的全部实例，用于跨实现精确比较。
func snapshotAll(view hooks.StateView, types ...string) map[string]map[string]map[string]any {
	out := make(map[string]map[string]map[string]any)
	for _, typ := range types {
		m := make(map[string]map[string]any)
		for _, id := range view.List(typ) {
			fields, _ := view.Get(typ, id)
			m[id] = fields
		}
		out[typ] = m
	}
	return out
}

// TestConcurrentOutermostActionsSerializable 验证并发发起的多个
// 最外层动作（各自含嵌套写入）的最终效果等价于按序号串行应用，
// 且任何事务内部都不会被其他事务的写入交错插入。
func TestConcurrentOutermostActionsSerializable(t *testing.T) {
	const n = 12
	sc := buildConcurrentScenario(n)

	var markerViews []markerObservation
	e := newConcurrentEngine(t, sc, &markerViews)

	execErrs := runConcurrentBatch(t, e, sc, 42)
	for i, err := range execErrs {
		if err != nil {
			t.Fatalf("动作 %d 执行失败: %v", i, err)
		}
	}

	// 对照模型：按序号顺序串行应用同一组动作。
	m := newConcurrentNaive(sc)
	for _, action := range sc.actions {
		if err := m.Execute(action); err != nil {
			t.Fatalf("naive 执行 %s 失败: %v", action, err)
		}
	}

	gotState := snapshotAll(e.State(), "marker", "ledger")
	wantState := snapshotAll(m.State(), "marker", "ledger")
	t.Logf("判定依据：并发执行结果必须等于按序号串行应用的朴素模型结果；"+
		"ledger.last 实际=%v 期望=%v，marker 数 实际=%d 期望=%d",
		gotState["ledger"]["main"], wantState["ledger"]["main"],
		len(gotState["marker"]), len(wantState["marker"]))
	if !reflect.DeepEqual(gotState, wantState) {
		t.Fatalf("最终状态与串行模型不一致\n实际: %v\n期望: %v", gotState, wantState)
	}
	// ledger 的最终 last 必须是序号最大的动作（串行顺序的最后一个）。
	if got := gotState["ledger"]["main"]["last"]; got != fmt.Sprintf("outer-%d", n-1) {
		t.Fatalf("ledger.last 应为 outer-%d，实际 %v", n-1, got)
	}

	// 无交错验证：任一前置钩子观察到的 marker 集合中，除当前事务
	// 自己尚未完成的第二个 marker 外，其他动作的 a/b 两个 marker
	// 必须成对出现（不允许看到他方事务的半成品写入）。
	for _, obs := range markerViews {
		set := make(map[string]bool, len(obs.present))
		for _, id := range obs.present {
			set[id] = true
		}
		for j := 0; j < n; j++ {
			aid := fmt.Sprintf("m-%d-a", j)
			bid := fmt.Sprintf("m-%d-b", j)
			a, b := set[aid], set[bid]
			if obs.writeID == bid && a && !b {
				continue // 当前事务自己：a 已写、b 正要写，属正常
			}
			if a != b {
				t.Fatalf("观察到动作 %d 的半成品写入（a=%v b=%v），事务边界被交错", j, a, b)
			}
		}
	}
	t.Logf("无交错验证通过：%d 次前置钩子观察中，所有他方事务的 marker 均成对出现",
		len(markerViews))
}

// TestReplayDeterministic 验证重放同一组最外层动作调用序列，
// 得到完全相同的最终状态与钩子触发记录。
func TestReplayDeterministic(t *testing.T) {
	const n = 10
	sc := buildConcurrentScenario(n)

	runOnce := func(shuffleSeed int64) (map[string]map[string]map[string]any, []hooks.Record) {
		var views []markerObservation
		e := newConcurrentEngine(t, sc, &views)
		runConcurrentBatch(t, e, sc, shuffleSeed)
		return snapshotAll(e.State(), "marker", "ledger"), e.HookLog()
	}

	state1, log1 := runOnce(1)
	state2, log2 := runOnce(999) // 不同的 goroutine 提交顺序
	t.Logf("判定依据：两轮重放（不同并发提交顺序、相同调用序号序列）"+
		"最终状态与钩子记录必须逐字节一致；钩子记录数 第一轮=%d 第二轮=%d",
		len(log1), len(log2))
	if !reflect.DeepEqual(state1, state2) {
		t.Fatal("两轮重放的最终状态不一致")
	}
	if !reflect.DeepEqual(log1, log2) {
		t.Fatalf("两轮重放的钩子触发记录不一致\n第一轮: %v\n第二轮: %v", log1, log2)
	}
}
