package ontology_test

import (
	"sync"
	"testing"

	"ontology/ontology"
)

func setN(n int) func(map[string]any) ontology.Mutation {
	return func(map[string]any) ontology.Mutation {
		return ontology.Mutation{Set: map[string]any{"n": n}}
	}
}

func incN(props map[string]any) ontology.Mutation {
	n, _ := props["n"].(int)
	return ontology.Mutation{Set: map[string]any{"n": n + 1}}
}

// 同权限多方竞争：不适用抢占，按到达顺序裁定，先到者生效、后到者普通冲突重试。
func TestSamePriorityContention(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exec := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			if a.ID == "A" {
				once.Do(func() { close(started); <-release })
			}
		},
	})
	actA := ontology.Action{ID: "A", ObjectID: "o1", Priority: 1, MaxRetries: 5, Apply: incN}
	actB := ontology.Action{ID: "B", ObjectID: "o1", Priority: 1, MaxRetries: 5, Apply: incN}

	resCh := make(chan ontology.Result, 1)
	go func() { resCh <- exec.Run(actA) }()
	<-started // A 已持有基线 0，停在提交前
	resB := exec.Run(actB)
	close(release)
	resA := <-resCh

	if resB.Final != ontology.Committed || len(resB.Attempts) != 1 {
		t.Fatalf("B: got %+v, want committed in one attempt", resB)
	}
	if resA.Final != ontology.Committed || len(resA.Attempts) != 2 {
		t.Fatalf("A: got %+v, want committed after one conflict retry", resA)
	}
	if resA.Attempts[0].Outcome != ontology.AttemptConflict {
		t.Fatalf("A attempt 0: got %v, want conflict (same priority never preempts)", resA.Attempts[0].Outcome)
	}
	for _, ev := range store.Events() {
		if ev.Kind == ontology.EventPreempted {
			t.Fatalf("same-priority contention must not produce preemption: %+v", ev)
		}
	}
	version, _, props := store.State("o1")
	if version != 2 || props["n"] != 2 {
		t.Fatalf("final state: version=%d n=%v, want version=2 n=2", version, props["n"])
	}
}

// 低权限动作在重试循环中被更高权限写入抢占：立即终止，不耗尽重试预算。
func TestHigherPriorityPreemptsRetryLoop(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})
	plain := ontology.NewExecutor(store, ontology.Hooks{})

	snapDone := make(chan struct{})
	allowM := make(chan struct{})
	parked := make(chan struct{})
	release := make(chan struct{})
	var once0, once1 sync.Once
	exec := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			if a.ID != "L" {
				return
			}
			switch baseline {
			case 0:
				once0.Do(func() { close(snapDone); <-allowM })
			case 1:
				once1.Do(func() { close(parked); <-release })
			}
		},
	})

	actL := ontology.Action{ID: "L", ObjectID: "o1", Priority: 1, MaxRetries: 10, Apply: setN(-1)}
	resCh := make(chan ontology.Result, 1)
	go func() { resCh <- exec.Run(actL) }()

	<-snapDone // L 已持有基线 0
	if res := plain.Run(ontology.Action{ID: "M", ObjectID: "o1", Priority: 1, MaxRetries: 0, Apply: setN(1)}); res.Final != ontology.Committed {
		t.Fatalf("M: got %+v, want committed", res)
	}
	close(allowM) // 放行后 L 的首次提交必因 M 的写入冲突
	<-parked      // L 冲突一次后持有基线 1，停在提交前
	if res := plain.Run(ontology.Action{ID: "H", ObjectID: "o1", Priority: 5, MaxRetries: 0, Apply: setN(100)}); res.Final != ontology.Committed {
		t.Fatalf("H: got %+v, want committed", res)
	}
	close(release)
	resL := <-resCh

	if resL.Final != ontology.Preempted {
		t.Fatalf("L: got final %v, want Preempted", resL.Final)
	}
	if len(resL.Attempts) != 2 ||
		resL.Attempts[0].Outcome != ontology.AttemptConflict ||
		resL.Attempts[1].Outcome != ontology.AttemptPreempted {
		t.Fatalf("L attempts: got %+v, want [conflict preempted]", resL.Attempts)
	}
	// 终止是立即的：仅消耗 1 次冲突重试，远未触及预算上限 10。
	version, clock, props := store.State("o1")
	if version != 2 || clock != 2 || props["n"] != 100 {
		t.Fatalf("final state: version=%d clock=%d n=%v, want 2/2/100 (L 不得产生额外变化)", version, clock, props["n"])
	}
}

// 不同权限交替抢占：p1 被 p3 抢占，p3 又被 p5 抢占，形成分层链。
func TestAlternatingPreemption(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})
	plain := ontology.NewExecutor(store, ontology.Hooks{})

	// X 先提交到 v1。
	if res := plain.Run(ontology.Action{ID: "X", ObjectID: "o1", Priority: 1, MaxRetries: 0, Apply: setN(1)}); res.Final != ontology.Committed {
		t.Fatalf("X: got %+v", res)
	}

	// 每个动作按尝试序号设置闸门：L 停 1 次，M2 停 2 次。
	parked := map[string][]chan struct{}{
		"L":  {make(chan struct{})},
		"M2": {make(chan struct{}), make(chan struct{})},
	}
	release := map[string][]chan struct{}{
		"L":  {make(chan struct{})},
		"M2": {make(chan struct{}), make(chan struct{})},
	}
	var mu sync.Mutex
	count := map[string]int{}
	exec := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			mu.Lock()
			idx := count[a.ID]
			count[a.ID]++
			mu.Unlock()
			if gates, ok := parked[a.ID]; ok && idx < len(gates) {
				close(gates[idx])
				<-release[a.ID][idx]
			}
		},
	})

	actL := ontology.Action{ID: "L", ObjectID: "o1", Priority: 1, MaxRetries: 10, Apply: setN(-1)}
	actM2 := ontology.Action{ID: "M2", ObjectID: "o1", Priority: 3, MaxRetries: 10, Apply: setN(-3)}
	resL := make(chan ontology.Result, 1)
	resM2 := make(chan ontology.Result, 1)
	go func() { resL <- exec.Run(actL) }()
	<-parked["L"][0] // L 持有基线 1
	go func() { resM2 <- exec.Run(actM2) }()
	<-parked["M2"][0] // M2 持有基线 1

	// M (p3) 提交 v2：抢占 L（p1）；对同级的 M2 不构成抢占。
	if res := plain.Run(ontology.Action{ID: "M", ObjectID: "o1", Priority: 3, MaxRetries: 0, Apply: setN(3)}); res.Final != ontology.Committed {
		t.Fatalf("M: got %+v", res)
	}
	close(release["L"][0])
	if got := <-resL; got.Final != ontology.Preempted {
		t.Fatalf("L: got %v, want Preempted (被 p3 抢占)", got.Final)
	}

	// M2 用基线 1 提交：版本落后（当前 v2）且同级写入不构成抢占，判普通冲突；
	// 随后停在基线 2 的第二次尝试前。
	close(release["M2"][0])
	<-parked["M2"][1]
	// H (p5) 提交 v3：M2 的基线被更高权限写入推进，应被抢占。
	if res := plain.Run(ontology.Action{ID: "H", ObjectID: "o1", Priority: 5, MaxRetries: 0, Apply: setN(5)}); res.Final != ontology.Committed {
		t.Fatalf("H: got %+v", res)
	}
	close(release["M2"][1])
	gotM2 := <-resM2
	if gotM2.Final != ontology.Preempted {
		t.Fatalf("M2: got %v, want Preempted (被 p5 抢占)", gotM2.Final)
	}
	if gotM2.Attempts[0].Outcome != ontology.AttemptConflict {
		t.Fatalf("M2 attempt 0: got %v, want conflict (同级写入不抢占)", gotM2.Attempts[0].Outcome)
	}

	version, _, props := store.State("o1")
	if version != 3 || props["n"] != 5 {
		t.Fatalf("final state: version=%d n=%v, want 3/5", version, props["n"])
	}
}

// 高权限动作自身不免疫冲突：与同级冲突按普通规则重试；
// 与更高权限冲突同样被抢占。
func TestHighPriorityActionNotImmune(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})
	plain := ontology.NewExecutor(store, ontology.Hooks{})

	parked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exec := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			if a.ID == "H2" {
				once.Do(func() { close(parked); <-release })
			}
		},
	})
	actH2 := ontology.Action{ID: "H2", ObjectID: "o1", Priority: 5, MaxRetries: 5, Apply: incN}
	resCh := make(chan ontology.Result, 1)
	go func() { resCh <- exec.Run(actH2) }()
	<-parked // H2 持有基线 0
	if res := plain.Run(ontology.Action{ID: "H1", ObjectID: "o1", Priority: 5, MaxRetries: 0, Apply: incN}); res.Final != ontology.Committed {
		t.Fatalf("H1: got %+v", res)
	}
	close(release)
	resH2 := <-resCh
	if resH2.Final != ontology.Committed || len(resH2.Attempts) != 2 ||
		resH2.Attempts[0].Outcome != ontology.AttemptConflict {
		t.Fatalf("H2: got %+v, want conflict retry then committed (同级不抢占)", resH2)
	}

	// 更高权限写入生效后，高权限动作同样被抢占。
	parked2 := make(chan struct{})
	release2 := make(chan struct{})
	var once2 sync.Once
	exec2 := ontology.NewExecutor(store, ontology.Hooks{
		BeforeCommit: func(a ontology.Action, baseline int64) {
			if a.ID == "H3" {
				once2.Do(func() { close(parked2); <-release2 })
			}
		},
	})
	actH3 := ontology.Action{ID: "H3", ObjectID: "o1", Priority: 5, MaxRetries: 5, Apply: setN(-5)}
	resCh2 := make(chan ontology.Result, 1)
	go func() { resCh2 <- exec2.Run(actH3) }()
	<-parked2 // H3 持有基线 2
	if res := plain.Run(ontology.Action{ID: "H4", ObjectID: "o1", Priority: 9, MaxRetries: 0, Apply: setN(9)}); res.Final != ontology.Committed {
		t.Fatalf("H4: got %+v", res)
	}
	close(release2)
	if resH3 := <-resCh2; resH3.Final != ontology.Preempted {
		t.Fatalf("H3: got %v, want Preempted (高权限不免疫更高权限)", resH3.Final)
	}
}

// 判定顺序：抢占判定先于重试预算耗尽判定。
func TestPreemptionPrecedesBudgetExhaustion(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})
	store.Create("o2", map[string]any{"n": 0})
	plain := ontology.NewExecutor(store, ontology.Hooks{})

	run := func(objID, actID string, committer ontology.Action) ontology.Result {
		parked := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		exec := ontology.NewExecutor(store, ontology.Hooks{
			BeforeCommit: func(a ontology.Action, baseline int64) {
				if a.ID == actID {
					once.Do(func() { close(parked); <-release })
				}
			},
		})
		resCh := make(chan ontology.Result, 1)
		go func() {
			resCh <- exec.Run(ontology.Action{ID: actID, ObjectID: objID, Priority: 1, MaxRetries: 0, Apply: setN(-1)})
		}()
		<-parked // 动作持有基线 0 且重试预算为 0
		if res := plain.Run(committer); res.Final != ontology.Committed {
			t.Fatalf("committer %s: got %+v", committer.ID, res)
		}
		close(release)
		return <-resCh
	}

	// 基线被更高权限写入推进：即使预算已耗尽，也判 Preempted 而非 RetriesExhausted。
	if res := run("o1", "A", ontology.Action{ID: "X", ObjectID: "o1", Priority: 9, MaxRetries: 0, Apply: setN(9)}); res.Final != ontology.Preempted {
		t.Fatalf("A: got %v, want Preempted (抢占先于预算耗尽)", res.Final)
	}
	// 基线被同级写入推进：判普通冲突，预算 0 直接耗尽。
	if res := run("o2", "B", ontology.Action{ID: "Y", ObjectID: "o2", Priority: 1, MaxRetries: 0, Apply: setN(1)}); res.Final != ontology.RetriesExhausted {
		t.Fatalf("B: got %v, want RetriesExhausted", res.Final)
	}
}

// 被抢占终止是纯查询：版本、属性、时钟均不发生额外变化。
func TestPreemptionIsPureQuery(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0, "tag": "keep"})
	plain := ontology.NewExecutor(store, ontology.Hooks{})
	if res := plain.Run(ontology.Action{ID: "H", ObjectID: "o1", Priority: 5, MaxRetries: 0, Apply: setN(5)}); res.Final != ontology.Committed {
		t.Fatalf("H: got %+v", res)
	}
	v0, c0, p0 := store.State("o1")
	events0 := len(store.Events())

	outcome, _ := store.TryCommit("o1", "L", 1, 0, ontology.Mutation{Set: map[string]any{"n": -1}})
	if outcome != ontology.AttemptPreempted {
		t.Fatalf("got %v, want AttemptPreempted", outcome)
	}
	v1, c1, p1 := store.State("o1")
	if v0 != v1 || c0 != c1 {
		t.Fatalf("preemption mutated state: version %d->%d clock %d->%d", v0, v1, c0, c1)
	}
	if p1["n"] != p0["n"] || p1["tag"] != p0["tag"] {
		t.Fatalf("preemption mutated props: %v -> %v", p0, p1)
	}
	events := store.Events()
	if len(events) != events0+1 || events[len(events)-1].Kind != ontology.EventPreempted {
		t.Fatalf("preemption must only append one preempt event, got %+v", events[events0:])
	}
}

// 抢占判定开销与并发动作总数无关：水位线规模只随权限等级数增长。
func TestPreemptCostIndependentOfContenders(t *testing.T) {
	store := ontology.NewStore()
	store.Create("o1", map[string]any{"n": 0})
	// 模拟大量动作竞争同一实例：顺序提交 5000 次，权限等级仅 4 种。
	for i := 0; i < 5000; i++ {
		snap := store.ReadSnapshot("o1")
		if outcome, _ := store.TryCommit("o1", "a", ontology.Priority(i%4), snap.Version, ontology.Mutation{
			Set: map[string]any{"n": i},
		}); outcome != ontology.AttemptCommitted {
			t.Fatalf("commit %d failed", i)
		}
	}
	if levels := store.WatermarkLevels("o1"); levels != 4 {
		t.Fatalf("watermark tracks %d levels after 5000 contenders, want 4 (与动作总数无关)", levels)
	}
}
