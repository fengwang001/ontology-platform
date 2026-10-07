package engine_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/engine"
	"ontology/errs"
	"ontology/hooks"
	"ontology/naive"
	"ontology/spec"
)

// diffTypes 是差分测试使用的对象类型集合。
var diffTypes = []string{"T0", "T1", "T2"}

func diffSchema() spec.Schema {
	return spec.Schema{Fields: map[string]spec.FieldType{
		"f0": spec.FieldInt,
		"f1": spec.FieldString,
	}}
}

// registerDiffHooks 在引擎与朴素模型上注册同一组确定性钩子：
// 失败与否完全由钩子看到的状态与写入决定。
func registerDiffHooks(
	regPre func(typ, name string, fn hooks.Func),
	regPost func(typ, name string, fn hooks.Func),
) {
	regPre("T0", "t0-f0-nonneg", func(ctx hooks.Context) error {
		if v, ok := ctx.Write.Fields["f0"].(int); ok && v < 0 {
			return fmt.Errorf("T0.f0 must be >= 0, got %d", v)
		}
		return nil
	})
	regPre("T1", "t1-cap-5", func(ctx hooks.Context) error {
		if ctx.Write.Op == spec.OpCreate && ctx.State.Count("T1") >= 5 {
			return fmt.Errorf("T1 count cap 5 reached (have %d)", ctx.State.Count("T1"))
		}
		return nil
	})
	regPost("T0", "t0-sum-limit", func(ctx hooks.Context) error {
		sum := 0
		for _, id := range ctx.State.List("T0") {
			fields, _ := ctx.State.Get("T0", id)
			if v, ok := fields["f0"].(int); ok {
				sum += v
			}
		}
		if sum > 200 {
			return fmt.Errorf("T0 f0 sum %d exceeds 200", sum)
		}
		return nil
	})
	regPost("T0", "t0-no-empty-f1", func(ctx hooks.Context) error {
		for _, id := range ctx.State.List("T0") {
			fields, _ := ctx.State.Get("T0", id)
			if fields["f1"] == "" {
				return fmt.Errorf("T0/%s has empty f1", id)
			}
		}
		return nil
	})
	regPost("T2", "t2-max-6", func(ctx hooks.Context) error {
		if ctx.State.Count("T2") > 6 {
			return fmt.Errorf("T2 count %d exceeds 6", ctx.State.Count("T2"))
		}
		return nil
	})
}

// diffScenario 是一次随机生成的差分测试输入。
type diffScenario struct {
	seed  int64
	defs  []spec.ActionDef
	calls []string
}

// genDiffScenario 生成随机嵌套动作序列：
// 动作体由随机写入与对更小下标动作的嵌套调用组成（DAG，深度有界），
// 写入含一定比例的非法参数（目标不存在、类型不符）。
func genDiffScenario(rng *rand.Rand, numActions, numCalls int) diffScenario {
	sc := diffScenario{seed: rng.Int63()}
	// create 从较大的池里取 ID（降低撞名概率）；
	// update/delete 从较小的热池里取（更可能命中已存在实例），
	// 使成功事务与各类失败都有充分覆盖。
	createPool := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		createPool = append(createPool, fmt.Sprintf("id-%d", i))
	}
	hotPool := createPool[:5]

	randWrite := func() spec.Op {
		typ := diffTypes[rng.Intn(len(diffTypes))]
		var fields map[string]any
		switch rng.Intn(20) {
		case 0: // 类型不符的非法写入
			fields = map[string]any{"f0": "not-an-int", "f1": "x"}
		case 1: // 未知字段的非法写入
			fields = map[string]any{"nope": 1}
		case 2, 3: // 触发 t0-f0-nonneg 前置钩子
			fields = map[string]any{"f0": -1 - rng.Intn(5), "f1": "neg"}
		case 4, 5: // 触发 t0-no-empty-f1 后置钩子
			fields = map[string]any{"f0": rng.Intn(30), "f1": ""}
		default:
			fields = map[string]any{"f0": rng.Intn(60), "f1": fmt.Sprintf("v%d", rng.Intn(100))}
		}
		var op spec.WriteOp
		var id string
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			op = spec.OpCreate
			id = createPool[rng.Intn(len(createPool))]
		case 5, 6, 7:
			op = spec.OpUpdate
			id = hotPool[rng.Intn(len(hotPool))]
		default:
			op = spec.OpDelete
			id = hotPool[rng.Intn(len(hotPool))]
		}
		return wr(typ, id, op, fields)
	}

	for i := 0; i < numActions; i++ {
		name := fmt.Sprintf("A%d", i)
		bodyLen := 1 + rng.Intn(4)
		body := make([]spec.Op, 0, bodyLen)
		for j := 0; j < bodyLen; j++ {
			if i > 0 && rng.Intn(3) == 0 {
				body = append(body, call(fmt.Sprintf("A%d", rng.Intn(i))))
			} else {
				body = append(body, randWrite())
			}
		}
		sc.defs = append(sc.defs, spec.ActionDef{Name: name, Body: body})
	}
	for i := 0; i < numCalls; i++ {
		sc.calls = append(sc.calls, fmt.Sprintf("A%d", rng.Intn(numActions)))
	}
	return sc
}

func renderScenario(sc diffScenario) string {
	out := fmt.Sprintf("seed=%d, %d 个动作定义, %d 次最外层调用\n", sc.seed, len(sc.defs), len(sc.calls))
	for _, def := range sc.defs {
		out += "  action " + def.Name + ":\n"
		for _, op := range def.Body {
			if op.Write != nil {
				w := op.Write
				out += fmt.Sprintf("    write %s %s/%s %v\n", w.Op, w.Type, w.ID, w.Fields)
			} else {
				out += "    call " + op.Call + "\n"
			}
		}
	}
	out += "  调用序列: "
	for _, c := range sc.calls {
		out += c + " "
	}
	return out
}

// TestRandomNestedSequencesMatchNaive 是核心差分测试：
// 在大量随机嵌套动作序列下，将正式引擎与独立维护事务日志、
// 逐步应用写入并在固定时点调用钩子的朴素模型逐条对照
// 每次调用的拒绝原因、最终状态与钩子触发记录。
func TestRandomNestedSequencesMatchNaive(t *testing.T) {
	const numSeeds = 40
	for seed := int64(0); seed < numSeeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sc := genDiffScenario(rng, 8, 40)

		e := engine.New()
		m := naive.New()
		for _, typ := range diffTypes {
			e.RegisterType(typ, diffSchema())
			m.RegisterType(typ, diffSchema())
		}
		registerDiffHooks(e.RegisterPreHook, e.RegisterPostHook)
		registerDiffHooks(m.RegisterPreHook, m.RegisterPostHook)

		// 引导动作：预置热池实例，使后续 update/delete 有命中目标，
		// 成功事务与各类失败都能得到充分覆盖。
		bootBody := make([]spec.Op, 0, 15)
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("id-%d", i)
			bootBody = append(bootBody,
				wr("T0", id, spec.OpCreate, map[string]any{"f0": 10, "f1": "boot"}),
				wr("T2", id, spec.OpCreate, map[string]any{"f0": 10, "f1": "boot"}),
				// T1 恰好到 t1-cap-5 前置钩子的上限
				wr("T1", id, spec.OpCreate, map[string]any{"f0": 10, "f1": "boot"}))
		}
		sc.defs = append(sc.defs, spec.ActionDef{Name: "boot", Body: bootBody})

		for _, def := range sc.defs {
			if err := e.RegisterAction(def); err != nil {
				t.Fatalf("seed=%d 引擎注册 %s 失败: %v", seed, def.Name, err)
			}
			if err := m.RegisterAction(def); err != nil {
				t.Fatalf("seed=%d 朴素模型注册 %s 失败: %v", seed, def.Name, err)
			}
		}

		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Logf("输入（随机生成的嵌套动作序列）:\n%s", renderScenario(sc))
			if err := e.Execute("boot"); err != nil {
				t.Fatalf("引擎引导失败: %v", err)
			}
			if err := m.Execute("boot"); err != nil {
				t.Fatalf("朴素模型引导失败: %v", err)
			}
			kindCount := make(map[errs.Kind]int)
			for i, action := range sc.calls {
				gotErr := e.Execute(action)
				wantErr := m.Execute(action)
				gotKind, wantKind := errs.KindOf(gotErr), errs.KindOf(wantErr)
				kindCount[gotKind]++
				if gotKind != wantKind {
					t.Fatalf("第 %d 次调用 %s：错误类别不一致\n输入见上\n引擎: %v\n朴素模型: %v",
						i, action, gotErr, wantErr)
				}
				gotMsg, wantMsg := errString(gotErr), errString(wantErr)
				if gotMsg != wantMsg {
					t.Fatalf("第 %d 次调用 %s：错误文本不一致\n引擎: %q\n朴素模型: %q",
						i, action, gotMsg, wantMsg)
				}
				if i < 5 || gotErr != nil {
					t.Logf("调用 #%d %s -> 类别=%v 结果=%q（两实现一致）",
						i, action, kindName(gotKind), gotMsg)
				}
			}

			gotState := snapshotAll(e.State(), diffTypes...)
			wantState := snapshotAll(m.State(), diffTypes...)
			if !reflect.DeepEqual(gotState, wantState) {
				t.Fatalf("最终状态不一致\n引擎: %v\n朴素模型: %v", gotState, wantState)
			}
			gotLog, wantLog := e.HookLog(), m.HookLog()
			if !reflect.DeepEqual(gotLog, wantLog) {
				t.Fatalf("钩子触发记录不一致（%d vs %d 条）\n引擎: %v\n朴素模型: %v",
					len(gotLog), len(wantLog), gotLog, wantLog)
			}
			t.Logf("判定依据：与独立朴素模型逐条对照——%d 次调用的错误类别与文本、"+
				"最终状态（%d 个类型）、%d 条钩子记录全部一致",
				len(sc.calls), len(diffTypes), len(gotLog))
			t.Logf("结果分布：成功=%d 参数非法=%d 前置钩子失败=%d 后置钩子失败=%d",
				kindCount[-1], kindCount[errs.KindInvalidArgument],
				kindCount[errs.KindPreHook], kindCount[errs.KindPostHook])
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func kindName(k errs.Kind) string {
	if k == -1 {
		return "ok"
	}
	return k.String()
}
