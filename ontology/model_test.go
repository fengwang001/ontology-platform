package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveModel 是独立实现的朴素一次性消费模型：用线性扫描做去重、
// 用切片模拟副作用提交，刻意不使用被测系统的任何组件，作为随机
// 操作序列下逐条对照的参照物。
type naiveModel struct {
	objects map[string]map[string]int
	seen    [][2]string // (eventID, actionExecutionID)，线性扫描
	comps   map[string]*naiveComp
}

type naiveComp struct {
	effects   []SideEffect
	committed []bool
	state     CompensationState
	errClass  ErrorClass
}

type naiveResult struct {
	outcome  Outcome
	errClass ErrorClass // 仅 outcome==OutcomeError 时有意义
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		objects: map[string]map[string]int{
			"a": {"balance": 0, "count": 0},
			"b": {"balance": 0},
		},
		comps: make(map[string]*naiveComp),
	}
}

// deliver 处理一条事件。applyLimit >= 0 时模拟“提交前 applyLimit 项后
// 崩溃”：只施加前 applyLimit 项副作用。
func (m *naiveModel) deliver(evt ChangeEvent, builders map[string]CompensationBuilder, applyLimit int) naiveResult {
	if evt.EventID == "" || evt.ActionExecutionID == "" {
		return naiveResult{OutcomeError, ErrIdentityUndecidable}
	}
	for _, pair := range m.seen {
		if pair[1] == evt.ActionExecutionID {
			if pair[0] == evt.EventID {
				return m.resume(evt)
			}
			return naiveResult{OutcomeError, ErrIdentityUndecidable}
		}
	}
	for _, pair := range m.seen {
		if pair[0] == evt.EventID {
			return naiveResult{OutcomeError, ErrIdentityUndecidable}
		}
	}
	m.seen = append(m.seen, [2]string{evt.EventID, evt.ActionExecutionID})

	if evt.Kind == KindActionReverted {
		return m.undo(evt)
	}
	return m.start(evt, builders, applyLimit)
}

func (m *naiveModel) resume(evt ChangeEvent) naiveResult {
	comp, ok := m.comps[evt.ActionExecutionID]
	if !ok {
		return naiveResult{OutcomeError, ErrHistoryMissing}
	}
	switch comp.state {
	case StateInProgress:
		if len(comp.effects) == 0 {
			comp.state = StateFailed
			comp.errClass = ErrHistoryMissing
			return naiveResult{OutcomeError, ErrHistoryMissing}
		}
		m.applyRemaining(comp)
		return naiveResult{OutcomeCompensated, 0}
	case StateFailed:
		return naiveResult{OutcomeError, comp.errClass}
	default:
		return naiveResult{OutcomeDuplicateSkipped, 0}
	}
}

func (m *naiveModel) undo(evt ChangeEvent) naiveResult {
	origID, _ := evt.Payload["OriginalActionExecutionID"].(string)
	if origID == "" {
		return naiveResult{OutcomeError, ErrIdentityUndecidable}
	}
	comp, ok := m.comps[origID]
	if !ok {
		m.comps[origID] = &naiveComp{state: StateAbandoned}
		return naiveResult{OutcomeAbandoned, 0}
	}
	if comp.state == StateCompleted || comp.state == StateFailed || comp.state == StateAbandoned {
		return naiveResult{OutcomeNoAction, 0}
	}
	committed := 0
	for _, c := range comp.committed {
		if c {
			committed++
		}
	}
	if committed == 0 {
		comp.state = StateAbandoned
		return naiveResult{OutcomeAbandoned, 0}
	}
	return naiveResult{OutcomeNoAction, 0}
}

func (m *naiveModel) start(evt ChangeEvent, builders map[string]CompensationBuilder, applyLimit int) naiveResult {
	if comp, ok := m.comps[evt.ActionExecutionID]; ok && comp.state == StateAbandoned {
		return naiveResult{OutcomeAbandoned, 0}
	}
	builder, registered := builders[evt.ActionType]
	if !registered {
		m.comps[evt.ActionExecutionID] = &naiveComp{state: StateCompleted}
		return naiveResult{OutcomeNoAction, 0}
	}
	effects, err := builder(evt)
	if err != nil {
		m.comps[evt.ActionExecutionID] = &naiveComp{state: StateFailed, errClass: ErrAtomicityViolation}
		return naiveResult{OutcomeError, ErrAtomicityViolation}
	}
	for _, e := range effects {
		if _, ok := m.objects[e.ObjectID]; !ok {
			m.comps[evt.ActionExecutionID] = &naiveComp{state: StateFailed, errClass: ErrTargetMissing}
			return naiveResult{OutcomeError, ErrTargetMissing}
		}
	}
	// 朴素结构校验（独立实现，不复用被测系统的 validatePlan）。
	valid := len(effects) > 0
	for _, e := range effects {
		known := e.Op == OpSet || e.Op == OpAdd || e.Op == OpDelete
		if !known || e.ObjectID == "" || (e.Op != OpDelete && e.Field == "") {
			valid = false
		}
	}
	if !valid {
		m.comps[evt.ActionExecutionID] = &naiveComp{state: StateFailed, errClass: ErrAtomicityViolation}
		return naiveResult{OutcomeError, ErrAtomicityViolation}
	}
	comp := &naiveComp{
		effects:   effects,
		committed: make([]bool, len(effects)),
		state:     StateInProgress,
	}
	m.comps[evt.ActionExecutionID] = comp
	limit := len(effects)
	crashed := false
	// 崩溃可能发生在任意一项提交之后（含最后一项提交之后、
	// 完成标记落盘之前），因此 applyLimit == limit 也算崩溃。
	if applyLimit >= 0 && applyLimit <= limit {
		limit = applyLimit
		crashed = true
	}
	for i := 0; i < limit; i++ {
		m.applyEffect(effects[i])
		comp.committed[i] = true
	}
	if crashed {
		return naiveResult{OutcomeError, 0} // 调用方按崩溃处理
	}
	comp.state = StateCompleted
	return naiveResult{OutcomeCompensated, 0}
}

func (m *naiveModel) applyRemaining(comp *naiveComp) {
	for i, e := range comp.effects {
		if comp.committed[i] {
			continue
		}
		m.applyEffect(e)
		comp.committed[i] = true
	}
	comp.state = StateCompleted
}

func (m *naiveModel) applyEffect(e SideEffect) {
	obj, ok := m.objects[e.ObjectID]
	if !ok {
		return
	}
	switch e.Op {
	case OpSet:
		obj[e.Field] = e.Value
	case OpAdd:
		obj[e.Field] += e.Value
	case OpDelete:
		delete(m.objects, e.ObjectID)
	}
}

// modelBuilders 是模型对照测试使用的补偿计划：副作用目标与参数全部
// 由事件 Payload 决定，便于随机生成。
func modelBuilders() map[string]CompensationBuilder {
	return map[string]CompensationBuilder{
		"move": func(evt ChangeEvent) ([]SideEffect, error) {
			from, _ := evt.Payload["from"].(string)
			to, _ := evt.Payload["to"].(string)
			amount, _ := evt.Payload["amount"].(int)
			return []SideEffect{
				{ObjectID: from, Op: OpAdd, Field: "balance", Value: -amount},
				{ObjectID: to, Op: OpAdd, Field: "balance", Value: amount},
				{ObjectID: from, Op: OpAdd, Field: "count", Value: 1},
			}, nil
		},
		"erase": func(evt ChangeEvent) ([]SideEffect, error) {
			target, _ := evt.Payload["target"].(string)
			return []SideEffect{{ObjectID: target, Op: OpDelete}}, nil
		},
	}
}

// TestModelConformance 在随机操作序列下，将被测消费结果与独立朴素
// 模型逐条对照：每步之后可观察对象状态与消费结果分类必须一致。
func TestModelConformance(t *testing.T) {
	seeds := []int64{1, 7, 42, 2026, 999983}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runModelConformance(t, rand.New(rand.NewSource(seed)), 600)
		})
	}
}

func runModelConformance(t *testing.T, rng *rand.Rand, steps int) {
	t.Helper()
	st := newTestStorage()
	builders := modelBuilders()
	consumer := NewConsumer(st, builders)
	model := newNaiveModel()

	objIDs := []string{"a", "b"}
	var history []ChangeEvent // 已成功投递（未崩溃）的事件，用于生成重复投递
	var actionSeq, eventSeq int
	consumes := 0

	newEvent := func() ChangeEvent {
		actionSeq++
		eventSeq++
		amount := rng.Intn(5) + 1
		from := objIDs[rng.Intn(len(objIDs))]
		to := objIDs[rng.Intn(len(objIDs))]
		actionType := "move"
		payload := map[string]any{"from": from, "to": to, "amount": amount}
		if rng.Intn(10) == 0 {
			actionType = "erase"
			payload = map[string]any{"target": from}
		}
		return ChangeEvent{
			EventID:           fmt.Sprintf("evt-%d", eventSeq),
			ActionExecutionID: fmt.Sprintf("act-%d", actionSeq),
			Kind:              KindActionSucceeded,
			ActionType:        actionType,
			Payload:           payload,
		}
	}

	compare := func(step int, want naiveResult, got ConsumeResult) {
		t.Helper()
		if got.Outcome != want.outcome {
			t.Fatalf("步骤 %d: 结果分类不一致：模型=%v 实际=%v (err=%v)",
				step, want.outcome, got.Outcome, got.Err)
		}
		if want.outcome == OutcomeError && want.errClass != 0 {
			if got.Err == nil || got.Err.Class != want.errClass {
				t.Fatalf("步骤 %d: 错误类别不一致：模型=%v 实际=%v",
					step, want.errClass, got.Err)
			}
		}
		snap := st.Store.Snapshot()
		if len(snap) != len(model.objects) {
			t.Fatalf("步骤 %d: 对象集合不一致：模型=%v 实际=%v",
				step, keysOf(model.objects), keysOfSnap(snap))
		}
		for id, fields := range model.objects {
			gotObj, ok := snap[id]
			if !ok || !reflect.DeepEqual(gotObj.Fields, fields) {
				t.Fatalf("步骤 %d: 对象 %q 状态不一致：模型=%v 实际=%v",
					step, id, fields, gotObj.Fields)
			}
		}
	}

	for step := 0; step < steps; step++ {
		choice := rng.Intn(100)
		switch {
		case choice < 45 || len(history) == 0:
			// 新事件（可能注入崩溃后重启续作）。
			evt := newEvent()
			crashAt := -1
			if rng.Intn(4) == 0 {
				crashAt = rng.Intn(3) + 1 // 在第 1..3 项副作用提交后崩溃
			}
			if crashAt >= 0 {
				c := NewConsumer(st, builders, WithCrashHook(
					func(id string, committed int) {
						if committed == crashAt {
							panic("模拟进程崩溃")
						}
					}))
				got, crashed := tryConsume(c, evt)
				mres := model.deliver(evt, builders, crashAt)
				consumes++
				if crashed {
					// 重启并重新投递同一事件。
					consumer = NewConsumer(st, builders)
					got = consumer.Consume(evt)
					consumes++
					compare(step, model.deliver(evt, builders, -1), got)
					history = append(history, evt)
					continue
				}
				// 崩溃点未触发（计划短于 crashAt）：按正常完成对照。
				compare(step, mres, got)
				history = append(history, evt)
				continue
			}
			got := consumer.Consume(evt)
			consumes++
			compare(step, model.deliver(evt, builders, -1), got)
			history = append(history, evt)

		case choice < 70:
			// 完全重复投递。
			evt := history[rng.Intn(len(history))]
			got := consumer.Consume(evt)
			consumes++
			compare(step, model.deliver(evt, builders, -1), got)

		case choice < 80:
			// 独立等价调用：参数相同，标识全新。
			base := history[rng.Intn(len(history))]
			actionSeq++
			eventSeq++
			evt := ChangeEvent{
				EventID:           fmt.Sprintf("evt-%d", eventSeq),
				ActionExecutionID: fmt.Sprintf("act-%d", actionSeq),
				Kind:              base.Kind,
				ActionType:        base.ActionType,
				Payload:           base.Payload,
			}
			got := consumer.Consume(evt)
			consumes++
			compare(step, model.deliver(evt, builders, -1), got)
			history = append(history, evt)

		case choice < 90:
			// 撤销某个已投递或尚未投递的动作。
			eventSeq++
			target := fmt.Sprintf("act-%d", rng.Intn(actionSeq+2)+1)
			undo := ChangeEvent{
				EventID:           fmt.Sprintf("evt-%d", eventSeq),
				ActionExecutionID: fmt.Sprintf("undo-%d", eventSeq),
				Kind:              KindActionReverted,
				Payload:           map[string]any{"OriginalActionExecutionID": target},
			}
			got := consumer.Consume(undo)
			consumes++
			compare(step, model.deliver(undo, builders, -1), got)
			history = append(history, undo)

		default:
			// 标识损坏的事件。
			eventSeq++
			evt := ChangeEvent{
				EventID:           fmt.Sprintf("evt-%d", eventSeq),
				ActionExecutionID: "",
				Kind:              KindActionSucceeded,
				ActionType:        "move",
				Payload:           map[string]any{"from": "a", "to": "b", "amount": 1},
			}
			got := consumer.Consume(evt)
			consumes++
			compare(step, model.deliver(evt, builders, -1), got)
		}
	}

	// 审计：每次消费都必须产生一条去重判定日志。
	if got := len(st.Dedup.Decisions()); got != consumes {
		t.Fatalf("决策日志条数应等于消费次数 %d，得到 %d", consumes, got)
	}
}

func keysOf(m map[string]map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfSnap(m map[string]Object) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
