package ontology_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/ontology"
	"ontology/tests/naive"
)

type sideEffect struct {
	hookID      string
	compensated bool
}

type prodWorld struct {
	mgr     *ontology.Manager
	ot      *ontology.ObjectType
	effects map[string][]sideEffect
	failSet map[string]bool
}

func hookFails(instID, from, to, hookID string, failSet map[string]bool) bool {
	return failSet[hookID+"|"+instID+"|"+from+"|"+to]
}

func buildProdWorld(t *testing.T, decl *naive.TypeDecl, failSet map[string]bool) *prodWorld {
	t.Helper()
	edges := make([][2]string, len(decl.Edges))
	for i, e := range decl.Edges {
		edges[i] = [2]string{e.From, e.To}
	}
	ot, err := ontology.NewObjectType("randtype", decl.Stages, edges, decl.Terminal)
	if err != nil {
		t.Fatalf("生产世界构造失败: %v", err)
	}
	w := &prodWorld{mgr: ontology.NewManager(), ot: ot, effects: make(map[string][]sideEffect), failSet: failSet}
	for _, h := range decl.Hooks {
		hid := h.ID
		semantic := ontology.CommitImmediately
		if h.Semantic == naive.SemanticCommitOnSuccess {
			semantic = ontology.CommitOnSuccess
		}
		hk := ontology.Hook{
			ID:       hid,
			Semantic: semantic,
			Check: func(c *ontology.TransitionContext) error {
				w.effects[c.InstanceID] = append(w.effects[c.InstanceID], sideEffect{hookID: hid})
				if hookFails(c.InstanceID, c.From, c.To, hid, w.failSet) {
					return fmt.Errorf("planned failure")
				}
				return nil
			},
			Compensate: func(c *ontology.TransitionContext) {
				effs := w.effects[c.InstanceID]
				for i := len(effs) - 1; i >= 0; i-- {
					if effs[i].hookID == hid && !effs[i].compensated {
						effs[i].compensated = true
						w.effects[c.InstanceID] = append(effs, sideEffect{hookID: hid, compensated: true})
						break
					}
				}
			},
		}
		if h.Kind == naive.KindTransition {
			ot.Registry().RegisterTransition(h.From, h.Stage, hk)
		} else {
			ot.Registry().RegisterEntry(h.Stage, hk)
		}
	}
	return w
}

func effectStrings(effs []sideEffect) []string {
	out := make([]string, len(effs))
	for i, e := range effs {
		tag := ":live"
		if e.compensated {
			tag = ":comp"
		}
		out[i] = e.hookID + tag
	}
	return out
}

func naiveEffectStrings(effs []naive.SideEffectEntry) []string {
	out := make([]string, len(effs))
	for i, e := range effs {
		tag := ":live"
		if e.Compensated {
			tag = ":comp"
		}
		out[i] = e.HookID + tag
	}
	return out
}

func fireStrings(recs []ontology.FireRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		tag := ":fire"
		if r.Compensated {
			tag = ":comp"
		}
		out[i] = r.HookID + tag
	}
	return out
}

func naiveFireStrings(recs []naive.FireEntry) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		tag := ":fire"
		if r.Compensated {
			tag = ":comp"
		}
		out[i] = r.HookID + tag
	}
	return out
}

func histEqual(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// randomDecl 生成随机类型声明：阶段、允许关系（含环/自环）、终态、两类钩子。
func randomDecl(rng *rand.Rand) *naive.TypeDecl {
	n := 2 + rng.Intn(5)
	stages := make([]string, n)
	for i := range stages {
		stages[i] = fmt.Sprintf("s%d", i)
	}
	edgeSet := map[[2]string]bool{}
	for i := 0; i < n+rng.Intn(n*2); i++ {
		edgeSet[[2]string{stages[rng.Intn(n)], stages[rng.Intn(n)]}] = true
	}
	var edges []naive.Edge
	for e := range edgeSet {
		edges = append(edges, naive.Edge{From: e[0], To: e[1]})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	var terminal []string
	for _, s := range stages {
		if rng.Float64() < 0.2 {
			terminal = append(terminal, s)
		}
	}
	hooks := []naive.HookSpec{}
	nextID := 0
	mkHook := func(kind naive.Kind, from, stage string, failSet map[string]bool) {
		id := fmt.Sprintf("h%d", nextID)
		nextID++
		semantic := naive.SemanticCommitImmediately
		if rng.Float64() < 0.5 {
			semantic = naive.SemanticCommitOnSuccess
		}
		hooks = append(hooks, naive.HookSpec{
			ID: id, Kind: kind, From: from, Stage: stage, Semantic: semantic,
			Fails: func(instID, f, t string) bool {
				return failSet[id+"|"+instID+"|"+f+"|"+t]
			},
		})
	}
	failSet := map[string]bool{}
	for _, e := range edges {
		if rng.Float64() < 0.6 {
			mkHook(naive.KindTransition, e.From, e.To, failSet)
		}
		if rng.Float64() < 0.2 {
			mkHook(naive.KindTransition, e.From, e.To, failSet)
		}
	}
	for _, s := range stages {
		if rng.Float64() < 0.5 {
			mkHook(naive.KindEntry, "", s, failSet)
		}
		if rng.Float64() < 0.2 {
			mkHook(naive.KindEntry, "", s, failSet)
		}
	}
	return &naive.TypeDecl{Stages: stages, Edges: edges, Terminal: terminal, Hooks: hooks}
}

// failSetOf 是两世界共享的确定性失败规则，需要跨闭包存活，故挂在场景外部。
func seedFailures(rng *rand.Rand, decl *naive.TypeDecl, instIDs []string) map[string]bool {
	failSet := map[string]bool{}
	for i := 0; i < 40; i++ {
		if len(decl.Hooks) == 0 {
			break
		}
		h := decl.Hooks[rng.Intn(len(decl.Hooks))]
		id := instIDs[rng.Intn(len(instIDs))]
		from := decl.Stages[rng.Intn(len(decl.Stages))]
		to := decl.Stages[rng.Intn(len(decl.Stages))]
		failSet[h.ID+"|"+id+"|"+from+"|"+to] = true
	}
	return failSet
}

// TestRandomDifferential 在大量随机声明/请求序列下逐条对照生产实现与朴素模型。
func TestRandomDifferential(t *testing.T) {
	const scenarios = 60
	const seqLen = 300
	rng := rand.New(rand.NewSource(20261007))

	totalRequests := 0
	for sc := 0; sc < scenarios; sc++ {
		decl := randomDecl(rng)

		instN := 1 + rng.Intn(3)
		instIDs := make([]string, instN)
		for i := range instIDs {
			instIDs[i] = fmt.Sprintf("inst%d", i)
		}
		failSet := seedFailures(rng, decl, instIDs)

		// 朴素模型的 Fails 闭包必须读到最终 failSet：重建声明中的闭包。
		for i := range decl.Hooks {
			h := &decl.Hooks[i]
			id := h.ID
			h.Fails = func(instID, f, to string) bool {
				return failSet[id+"|"+instID+"|"+f+"|"+to]
			}
		}

		prod := buildProdWorld(t, decl, failSet)
		ref := naive.NewModel(decl)
		for _, id := range instIDs {
			init := decl.Stages[rng.Intn(len(decl.Stages))]
			if _, err := prod.mgr.CreateInstance(prod.ot, id, init); err != nil {
				t.Fatalf("生产世界建实例失败: %v", err)
			}
			if r := ref.Create(id, init); r != naive.ReasonOK {
				t.Fatalf("朴素世界建实例失败: %s", r)
			}
		}

		type request struct{ id, to string }
		requests := make([]request, 0, seqLen)
		for i := 0; i < seqLen; i++ {
			id := instIDs[rng.Intn(len(instIDs))]
			if rng.Intn(10) == 0 {
				requests = append(requests, request{id, "undeclared-stage"})
				continue
			}
			if rng.Intn(20) == 0 {
				id = "ghost"
			}
			requests = append(requests, request{id, decl.Stages[rng.Intn(len(decl.Stages))]})
		}

		if sc < 3 {
			t.Logf("场景 %d: stages=%v edges=%v terminal=%v hooks=%d failRules=%d",
				sc, decl.Stages, decl.Edges, decl.Terminal, len(decl.Hooks), len(failSet))
		}

		for step, req := range requests {
			totalRequests++
			pRes, pErr := prod.mgr.Transition(req.id, req.to)
			rRes := ref.Transition(req.id, req.to)

			pReason, pFailedHook := string(naive.ReasonOK), ""
			if pErr != nil {
				le, _ := ontology.AsLifecycleError(pErr)
				pReason = string(le.Code)
				pFailedHook = le.HookID
			}
			basis := fmt.Sprintf("朴素模型全量遍历判定 %s（失败钩子 %q）", rRes.Reason, rRes.FailedHook)
			if sc < 3 && step < 12 {
				var fired []string
				if pRes != nil {
					fired = pRes.Fired
				}
				fmt.Printf("输入: [%d.%d] %s -> %s\n实际输出: reason=%s failedHook=%q fired=%v\n判定依据: %s\n",
					sc, step, req.id, req.to, pReason, pFailedHook, fired, basis)
			}

			if pReason != string(rRes.Reason) || pFailedHook != rRes.FailedHook {
				t.Fatalf("场景 %d 步 %d 请求 %v: 拒绝原因不一致 生产=%s/%q 朴素=%s/%q",
					sc, step, req, pReason, pFailedHook, rRes.Reason, rRes.FailedHook)
			}

			pIn := prod.mgr.GetInstance(req.id)
			rSnap, rExists := ref.Snapshot(req.id)
			if (pIn != nil) != rExists {
				t.Fatalf("场景 %d 步 %d: 实例存在性不一致", sc, step)
			}
			if pIn == nil {
				continue
			}
			var pHistory [][2]string
			for _, h := range pIn.History() {
				pHistory = append(pHistory, [2]string{h.From, h.To})
			}
			if pIn.Current() != rSnap.Current ||
				!histEqual(pHistory, rSnap.History) ||
				!strSlicesEqual(fireStrings(pIn.FireLog()), naiveFireStrings(rSnap.FireLog)) ||
				!strSlicesEqual(effectStrings(prod.effects[req.id]), naiveEffectStrings(rSnap.SideEffect)) {
				t.Fatalf("场景 %d 步 %d 请求 %v 可观测状态分叉:\n生产 current=%s history=%v\nfire=%v\neffect=%v\n朴素 current=%s history=%v\nfire=%v\neffect=%v",
					sc, step, req,
					pIn.Current(), pHistory, fireStrings(pIn.FireLog()), effectStrings(prod.effects[req.id]),
					rSnap.Current, rSnap.History, naiveFireStrings(rSnap.FireLog), naiveEffectStrings(rSnap.SideEffect))
			}
		}
	}
	t.Logf("差分测试完成：共对照 %d 条随机转移请求", totalRequests)
}
