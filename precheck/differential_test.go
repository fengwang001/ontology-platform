package precheck

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomDifferential 用大量随机操作序列对两个实现做逐条对照：
// 真实调用、版本演进、权限关系调整与假设性预检交错出现，
// 每次引擎预检都必须等于“朴素重演在该时刻切下快照后独立演算”的结果。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	reg := NewRegistry()

	// 预先注册一批行为各异、但对所有历史版本都存在的钩子实现。
	type hspec struct{ ref HookRef }
	var preSpecs, postSpecs []hspec
	mk := func(id string, preFail, postFail bool) {
		reg.Register(&testHook{id: id, preFail: preFail, postFail: postFail})
		if preFail {
			preSpecs = append(preSpecs, hspec{HookRef{id}})
		} else {
			preSpecs = append(preSpecs, hspec{HookRef{id}})
		}
		if postFail {
			postSpecs = append(postSpecs, hspec{HookRef{id}})
		} else {
			postSpecs = append(postSpecs, hspec{HookRef{id}})
		}
	}
	for i := 0; i < 6; i++ {
		mk(fmt.Sprintf("preok%d", i), false, false)
		mk(fmt.Sprintf("prebad%d", i), true, false)
		mk(fmt.Sprintf("postok%d", i), false, false)
		mk(fmt.Sprintf("postbad%d", i), false, true)
	}

	e := NewEngine(reg)
	n := NewNaiveReplay(reg)

	nodes := []string{"u1", "u2", "g1", "g2", "P"}
	const typ = "ActionX"

	apply := func(ev Event) {
		t.Helper()
		if err := e.Apply(ev); err != nil {
			t.Fatalf("seed=%d apply: %v", seed, err)
		}
		n.Append(ev)
	}

	at := Moment(1)

	// 初始世界：类型、用户、若干继承边、初版钩子集合。
	apply(Event{At: at, Kind: EvTypeSchema, ActionType: typ, Schema: &ParamSchema{
		Version: at, RequiredPermission: "P"}})
	apply(Event{At: at, Kind: EvUserAdded, User: "u1"})
	apply(Event{At: at, Kind: EvUserAdded, User: "u2"})
	apply(Event{At: at, Kind: EvEdgeAdded, From: "u1", To: "g1"})
	apply(Event{At: at, Kind: EvEdgeAdded, From: "g1", To: "P"})
	apply(Event{At: at, Kind: EvHookSet, ActionType: typ, HookSet: &HookSet{Version: at,
		Pre: []HookRef{{ID: "preok0"}}}})

	pick := func(specs []hspec) HookRef { return specs[rng.Intn(len(specs))].ref }

	checks := 0
	for step := 0; step < 220; step++ {
		at += Moment(1 + rng.Intn(3))
		switch rng.Intn(9) {
		case 0, 1: // 权限继承关系调整
			from := nodes[rng.Intn(4)] // 不动 P 的出边，避免无意义传播
			to := nodes[rng.Intn(len(nodes))]
			kind := EvEdgeAdded
			if rng.Intn(2) == 0 {
				kind = EvEdgeRemoved
			}
			apply(Event{At: at, Kind: kind, From: from, To: to})
		case 2: // 钩子版本演进：随机重排前置/后置集合
			hs := &HookSet{Version: at}
			for k := 0; k < rng.Intn(3); k++ {
				hs.Pre = append(hs.Pre, pick(preSpecs))
			}
			for k := 0; k < rng.Intn(3); k++ {
				hs.Post = append(hs.Post, pick(postSpecs))
			}
			apply(Event{At: at, Kind: EvHookSet, ActionType: typ, HookSet: hs})
		case 3: // 参数结构约束随时间演进
			schema := &ParamSchema{Version: at, RequiredPermission: "P"}
			if rng.Intn(2) == 0 {
				schema.Required = []string{"object_key"}
			}
			if rng.Intn(2) == 0 {
				schema.Types = map[string]string{"n": "int"}
			}
			apply(Event{At: at, Kind: EvTypeSchema, ActionType: typ, Schema: schema})
		case 4: // 对象世界变化
			key := nodes[rng.Intn(2)]
			apply(Event{At: at, Kind: EvObjectUpsert, ObjectKey: "obj-" + key, Value: at})
		case 5: // 真实调用（会落地副作用），随后其结果必须与同刻预检一致
			caller := nodes[rng.Intn(2)]
			params := randomParams(rng)
			agg := randomAgg(rng)
			execRes, err := e.Execute(at, typ, caller, params, agg)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			n.Load(e.Events())
			hypRes, _, err := n.Precheck(PrecheckRequest{At: at, ActionType: typ, Caller: caller, Params: params, Aggregation: agg})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(Canonical(execRes), Canonical(hypRes)) {
				t.Fatalf("seed=%d step=%d real-vs-hypothetical mismatch\nreal=%#v\nhyp =%#v",
					seed, step, Canonical(execRes), Canonical(hypRes))
			}
		default: // 假设性预检：引擎与朴素模型逐条对照
			caller := nodes[rng.Intn(2)]
			req := PrecheckRequest{
				At:          at - Moment(rng.Intn(3)), // 有时查恰好边界，有时查略早
				ActionType:  typ,
				Caller:      caller,
				Params:      randomParams(rng),
				Aggregation: randomAgg(rng),
			}
			res, err := e.Precheck(req)
			if err != nil {
				t.Fatal(err)
			}
			nres, nsnap, err := n.Precheck(req)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(Canonical(res), Canonical(nres)) {
				t.Fatalf("seed=%d step=%d engine/naive mismatch\nengine=%#v\nnaive=%#v",
					seed, step, Canonical(res), Canonical(nres))
			}
			// 权限快照也必须一致（边集与持有集合）。
			entry, ok := e.Audit().Get(res.AuditSeq)
			if !ok {
				t.Fatal("missing audit entry")
			}
			if !reflect.DeepEqual(entry.PermSnap.Active, nsnap.Active) ||
				!reflect.DeepEqual(entry.PermSnap.Held, nsnap.Held) {
				t.Fatalf("seed=%d step=%d permission snapshot mismatch", seed, step)
			}
			e.Audit().Annotate(res.AuditSeq, CrossCheckRecord{Equivalent: true, Note: "random differential"})
			checks++
		}
	}

	if checks < 50 {
		t.Fatalf("seed=%d produced too few precheck comparisons: %d", seed, checks)
	}

	// 审计完备性：每条预检审计都带输入、依据版本、权限快照与对照结论。
	for _, en := range e.Audit().Entries() {
		if en.Request.ActionType != typ {
			continue
		}
		if en.PermSnap == nil || en.CrossCheck == nil || !en.CrossCheck.Equivalent {
			t.Fatalf("seed=%d incomplete audit entry: %+v", seed, en)
		}
	}
}

func randomParams(rng *rand.Rand) map[string]any {
	params := map[string]any{}
	if rng.Intn(2) == 0 {
		params["object_key"] = "o1"
	}
	if rng.Intn(2) == 0 {
		if rng.Intn(2) == 0 {
			params["n"] = rng.Intn(10)
		} else {
			params["n"] = "bad-int"
		}
	}
	return params
}

func randomAgg(rng *rand.Rand) FailureAggregation {
	if rng.Intn(2) == 0 {
		return FailFast
	}
	return CollectAll
}

// TestSnapshotRebuildSublinear 可独立验证：
// 某调用者链路上发生海量版本演进/关系调整后，该调用者一次快照重建的
// 点查成本（以 SegmentChecks 计）不随演进总次数线性增长，
// 因为点查只二分“被访问键”各自的段历史，与系统全局演进总量无关。
func TestSnapshotRebuildSublinear(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testHook{id: "h"})
	e := NewEngine(reg)

	mustApply(t, e, Event{At: 1, Kind: EvTypeSchema, ActionType: "A", Schema: &ParamSchema{Version: 1, RequiredPermission: "P"}})
	mustApply(t, e, Event{At: 1, Kind: EvUserAdded, User: "u"})
	mustApply(t, e, Event{At: 1, Kind: EvEdgeAdded, From: "u", To: "P"})

	// 在与调用者 u 无关的键 g 上制造海量关系调整（系统累计演进总次数线性膨胀）。
	const churn = 50_000
	for i := 0; i < churn; i++ {
		at := Moment(200_000 + i)
		kind := EvEdgeAdded
		if i%2 == 1 {
			kind = EvEdgeRemoved
		}
		mustApply(t, e, Event{At: at, Kind: kind, From: "g", To: "P"})
	}

	e.resetStats()
	res, err := e.Precheck(PrecheckRequest{
		At: 200_000 + churn + 1, ActionType: "A", Caller: "u",
		Params: map[string]any{"object_key": "o"}, Aggregation: FailFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictAllowed {
		t.Fatalf("want allowed, got %s", res.Verdict)
	}
	entry, _ := e.Audit().Get(res.AuditSeq)
	checks := entry.Metrics["edge_segment_checks"]
	// u 与 P 两个被访问键各只有 1 段：线性扫描需要与 50000 同阶的访问，
	// 二分点查只需要个位数的段比较。
	if checks > 10 {
		t.Fatalf("snapshot rebuild scaled with total evolution count: %d segment checks", checks)
	}

	// 额外验证：当调用者自身的继承键有大量版本时，其单次点查代价是 O(log n)。
	e2 := NewEngine(reg)
	mustApply(t, e2, Event{At: 1, Kind: EvUserAdded, User: "u"})
	for i := 0; i < churn; i++ {
		at := Moment(200_000 + i)
		kind := EvEdgeAdded
		if i%2 == 1 {
			kind = EvEdgeRemoved
		}
		mustApply(t, e2, Event{At: at, Kind: kind, From: "u", To: "P"})
	}
	e2.resetStats()
	e2.edgeState.Point(edgeKey("u", "P"), 200_000+churn+1)
	if e2.edgeState.Stats().SegmentChecks > 20 {
		t.Fatalf("point check on high-churn key must be O(log n): %d", e2.edgeState.Stats().SegmentChecks)
	}
}
