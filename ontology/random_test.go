package ontology_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/ontology"
	"ontology/refmodel"
)

// 随机多权限等级并发操作序列 vs 独立分层串行参照模型：
// 两者在日志给出的等价串行顺序下必须完全一致，
// 且每条判定（冲突/抢占/提交）都被参照模型独立复核。
func TestRandomizedAgainstReferenceModel(t *testing.T) {
	var totalPreempted, totalExhausted, totalConflicts, totalCommitted int
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		store := ontology.NewStore()
		initial := map[string]map[string]any{}
		for _, obj := range []string{"o0", "o1", "o2"} {
			props := map[string]any{"count": 0, "tag": "init"}
			store.Create(obj, props)
			initial[obj] = map[string]any{"count": 0, "tag": "init"}
		}

		const nActions = 40
		exec := ontology.NewExecutor(store, ontology.Hooks{})
		actions := make(map[string]ontology.Action, nActions)
		results := make([]ontology.Result, nActions)
		var wg sync.WaitGroup
		for i := 0; i < nActions; i++ {
			id := fmt.Sprintf("s%d-a%d", seed, i)
			obj := fmt.Sprintf("o%d", rng.Intn(3))
			act := ontology.Action{
				ID:         id,
				ObjectID:   obj,
				Priority:   ontology.Priority(rng.Intn(4)),
				MaxRetries: rng.Intn(5),
				Apply: func(props map[string]any) ontology.Mutation {
					n, _ := props["count"].(int)
					return ontology.Mutation{Set: map[string]any{"count": n + 1, "last": id}}
				},
			}
			actions[id] = act
			wg.Add(1)
			go func(i int, act ontology.Action) {
				defer wg.Done()
				results[i] = exec.Run(act)
			}(i, act)
		}
		wg.Wait()

		rep := refmodel.Validate(initial, store.Events(), results, actions)
		if len(rep.Errors) > 0 {
			t.Fatalf("seed %d: reference model rejected log:\n%v", seed, rep.Errors)
		}
		for obj, want := range rep.Final {
			version, clock, props := store.State(obj)
			if version != want.Version || clock != want.Clock || !reflect.DeepEqual(props, want.Props) {
				t.Fatalf("seed %d object %s: store=(%d,%d,%v) refmodel=(%d,%d,%v)",
					seed, obj, version, clock, props, want.Version, want.Clock, want.Props)
			}
		}
		for _, pre := range rep.Preemptions {
			if pre.BySeq >= pre.Seq {
				t.Fatalf("seed %d: preemption of %s not preceded by its evidence", seed, pre.ActionID)
			}
		}
		for _, res := range results {
			switch res.Final {
			case ontology.Committed:
				totalCommitted++
			case ontology.Preempted:
				totalPreempted++
			case ontology.RetriesExhausted:
				totalExhausted++
			}
			for _, att := range res.Attempts {
				if att.Outcome == ontology.AttemptConflict {
					totalConflicts++
				}
			}
		}
	}
	t.Logf("totals: committed=%d preempted=%d exhausted=%d conflict-attempts=%d",
		totalCommitted, totalPreempted, totalExhausted, totalConflicts)
	// 覆盖性：随机序列必须真实触发全部四类结果。
	if totalCommitted == 0 || totalPreempted == 0 || totalExhausted == 0 || totalConflicts == 0 {
		t.Fatalf("randomized run did not cover all outcomes: committed=%d preempted=%d exhausted=%d conflicts=%d",
			totalCommitted, totalPreempted, totalExhausted, totalConflicts)
	}
}

// 抢占判定开销证据：同一实例上已生效写入总数从 10 增长到 100000，
// 单次抢占判定耗时应保持平坦（只取决于权限等级数 4）。
func BenchmarkPreemptCheck(b *testing.B) {
	for _, commits := range []int{10, 1_000, 100_000} {
		b.Run(fmt.Sprintf("commits=%d", commits), func(b *testing.B) {
			store := ontology.NewStore()
			store.Create("o1", map[string]any{"n": 0})
			for i := 0; i < commits; i++ {
				snap := store.ReadSnapshot("o1")
				store.TryCommit("o1", "w", ontology.Priority(i%4), snap.Version, ontology.Mutation{
					Set: map[string]any{"n": i},
				})
			}
			mut := ontology.Mutation{Set: map[string]any{"n": -1}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 基线 0 必然被更高权限写入推进：每次调用都是一次纯抢占判定。
				store.TryCommit("o1", "victim", 0, 0, mut)
			}
		})
	}
}
