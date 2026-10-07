package ontology_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

func ticketType() *ontology.ObjectType {
	return &ontology.ObjectType{
		Name: "ticket",
		Properties: []ontology.PropertySpec{
			{Name: "status", Mergeable: false},
			{Name: "assignee", Mergeable: false},
			{Name: "priority", Mergeable: true, MergeRule: ontology.RuleMaxInt},
			{Name: "tags", Mergeable: true, MergeRule: ontology.RuleSetUnion},
			{Name: "summary", Mergeable: true, MergeRule: ontology.RuleLWW},
		},
	}
}

func ticketInitial() map[string]ontology.Value {
	return map[string]ontology.Value{
		"status":   "open",
		"assignee": "nobody",
		"priority": int64(0),
		"tags":     []string{},
		"summary":  ontology.LWWValue{},
	}
}

var allProps = []string{"status", "assignee", "priority", "tags", "summary"}

// randomChanges 生成随机写入内容，覆盖纯可合并、纯不可合并与混合请求。
func randomChanges(r *rand.Rand) map[string]ontology.Value {
	n := 1 + r.Intn(len(allProps))
	perm := r.Perm(len(allProps))
	out := make(map[string]ontology.Value, n)
	for i := 0; i < n; i++ {
		switch allProps[perm[i]] {
		case "status":
			out["status"] = []string{"open", "closed", "done"}[r.Intn(3)]
		case "assignee":
			out["assignee"] = []string{"alice", "bob", "nobody"}[r.Intn(3)]
		case "priority":
			out["priority"] = int64(r.Intn(10))
		case "tags":
			m := r.Intn(3)
			tags := make([]string, 0, m)
			for j := 0; j < m; j++ {
				tags = append(tags, string(rune('a'+rune(r.Intn(5)))))
			}
			out["tags"] = tags
		case "summary":
			out["summary"] = ontology.LWWValue{
				Clock:  uint64(r.Intn(6)),
				Writer: fmt.Sprintf("w%d", r.Intn(3)),
				Data:   fmt.Sprintf("d%d", r.Intn(4)),
			}
		}
	}
	return out
}

// randomBase 生成基线版本：多数为当前或近期版本，少数为未来版本。
func randomBase(r *rand.Rand, current uint64) uint64 {
	switch r.Intn(10) {
	case 0:
		return current + 1 // 未来基线 → 基线冲突
	case 1, 2:
		if current >= 3 {
			return current - uint64(r.Intn(4)) // 较旧基线
		}
		return 0
	default:
		if current > 0 && r.Intn(2) == 0 {
			return current - uint64(r.Intn(2))
		}
		return current
	}
}

// TestRandomizedAgainstNaive 随机并发写序列与独立朴素模型对照：
// 不可合并属性的冲突判定结果必须完全一致，可合并属性的最终合并结果
// 也必须完全一致；同时验证判定开销与历史无关、日志可重放。
func TestRandomizedAgainstNaive(t *testing.T) {
	for _, retention := range []uint64{0, 4} {
		t.Run(fmt.Sprintf("retention=%d", retention), func(t *testing.T) {
			r := rand.New(rand.NewSource(20261008))
			store := ontology.NewStore(ontology.WithRetentionWindow(retention))
			typ := ticketType()
			initial := ticketInitial()
			if err := store.CreateInstance("T1", typ, initial); err != nil {
				t.Fatal(err)
			}
			model := naive.New(typ, initial, retention)

			var wantPropReads int64
			const rounds = 3000
			for i := 0; i < rounds; i++ {
				version, _, _ := store.Snapshot("T1")
				req := ontology.WriteRequest{
					RequestID:   fmt.Sprintf("r%d", i),
					InstanceID:  "T1",
					BaseVersion: randomBase(r, version),
					Changes:     randomChanges(r),
				}
				got, err := store.Apply(req)
				if err != nil {
					t.Fatalf("第 %d 轮 Apply 出错: %v", i, err)
				}
				want := model.Apply(req)
				if got.Outcome != want.Outcome {
					t.Fatalf("第 %d 轮结果不一致: store=%s naive=%s req=%+v",
						i, got.Outcome, want.Outcome, req)
				}
				if got.Outcome == ontology.OutcomeRejectedConflict &&
					got.ConflictProperty != want.ConflictProperty {
					t.Fatalf("第 %d 轮冲突属性不一致: store=%s naive=%s",
						i, got.ConflictProperty, want.ConflictProperty)
				}
				if got.Outcome != ontology.OutcomeRejectedStaleBaseline {
					for name := range req.Changes {
						if spec, _ := typ.Property(name); !spec.Mergeable {
							wantPropReads++
						}
					}
				}
			}

			// 最终版本与全部属性值一致。
			sv, svals, _ := store.Snapshot("T1")
			if sv != model.Version() {
				t.Fatalf("最终版本不一致: store=%d naive=%d", sv, model.Version())
			}
			for _, name := range allProps {
				if !ontology.Equal(svals[name], model.Value(name)) {
					t.Fatalf("属性 %s 最终值不一致: store=%v naive=%v",
						name, svals[name], model.Value(name))
				}
			}

			// 判定开销证据：优化实现零历史扫描、每属性版本号读取次数精确；
			// 朴素模型则随历史增长。
			if store.HistoryScans() != 0 {
				t.Fatalf("优化实现发生了 %d 次历史扫描", store.HistoryScans())
			}
			if store.PropVersionReads() != wantPropReads {
				t.Fatalf("属性版本号读取 %d 次，期望 %d 次",
					store.PropVersionReads(), wantPropReads)
			}
			if model.HistoryScans() == 0 {
				t.Fatal("朴素模型应当发生了历史扫描")
			}

			// 日志可独立重放核验。
			if err := ontology.Verify(typ, initial, retention, store.Log().Entries()); err != nil {
				t.Fatalf("日志重放核验失败: %v", err)
			}
		})
	}
}

// TestConcurrentSerializability 真实并发写入：最终状态必须等价于
// 按日志顺序（即实际串行化顺序）逐一执行的结果。
func TestConcurrentSerializability(t *testing.T) {
	const instances = 3
	const writers = 8
	const writesPerWriter = 200

	store := ontology.NewStore()
	typ := ticketType()
	initial := ticketInitial()
	ids := []string{"T1", "T2", "T3"}
	for _, id := range ids[:instances] {
		if err := store.CreateInstance(id, typ, initial); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < writesPerWriter; i++ {
				id := ids[r.Intn(instances)]
				version, _, _ := store.Snapshot(id)
				_, err := store.Apply(ontology.WriteRequest{
					RequestID:   fmt.Sprintf("g%d-%d", seed, i),
					InstanceID:  id,
					BaseVersion: randomBase(r, version),
					Changes:     randomChanges(r),
				})
				if err != nil {
					t.Errorf("Apply 出错: %v", err)
					return
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()

	// 按实例过滤日志，逐条与朴素模型对照。
	entries := store.Log().Entries()
	perInstance := make(map[string][]ontology.LogEntry)
	for _, e := range entries {
		perInstance[e.Request.InstanceID] = append(perInstance[e.Request.InstanceID], e)
	}
	for _, id := range ids[:instances] {
		model := naive.New(typ, initial, 0)
		for _, e := range perInstance[id] {
			want := model.Apply(e.Request)
			if e.Outcome != want.Outcome {
				t.Fatalf("实例 %s seq=%d 结果不一致: log=%s naive=%s",
					id, e.Seq, e.Outcome, want.Outcome)
			}
			if e.Outcome == ontology.OutcomeRejectedConflict &&
				e.ConflictProperty != want.ConflictProperty {
				t.Fatalf("实例 %s seq=%d 冲突属性不一致", id, e.Seq)
			}
		}
		sv, svals, _ := store.Snapshot(id)
		if sv != model.Version() {
			t.Fatalf("实例 %s 最终版本不一致: store=%d naive=%d", id, sv, model.Version())
		}
		for _, name := range allProps {
			if !ontology.Equal(svals[name], model.Value(name)) {
				t.Fatalf("实例 %s 属性 %s 最终值不一致: store=%v naive=%v",
					id, name, svals[name], model.Value(name))
			}
		}
		// 日志重放核验。
		if err := ontology.Verify(typ, initial, 0, perInstance[id]); err != nil {
			t.Fatalf("实例 %s 日志重放核验失败: %v", id, err)
		}
	}
}

// TestPermutationAgainstNaive 同一组写入的全部排列：最终属性值与
// 朴素模型逐顺序对照一致，且各排列间属性值完全相同。
func TestPermutationAgainstNaive(t *testing.T) {
	typ := ticketType()
	initial := ticketInitial()
	writes := []ontology.WriteRequest{
		{RequestID: "a", InstanceID: "T1", BaseVersion: 0, Changes: map[string]ontology.Value{
			"priority": int64(3), "tags": []string{"x"}, "status": "closed",
		}},
		{RequestID: "b", InstanceID: "T1", BaseVersion: 0, Changes: map[string]ontology.Value{
			"priority": int64(7), "tags": []string{"y"},
			"summary": ontology.LWWValue{Clock: 5, Writer: "bob", Data: "w"},
		}},
		{RequestID: "c", InstanceID: "T1", BaseVersion: 0, Changes: map[string]ontology.Value{
			"priority": int64(5), "tags": []string{"z", "x"},
		}},
		{RequestID: "d", InstanceID: "T1", BaseVersion: 0, Changes: map[string]ontology.Value{
			"tags": []string{"w"}, "summary": ontology.LWWValue{Clock: 2, Writer: "amy", Data: "v"},
		}},
	}

	var wantVals map[string]ontology.Value
	first := true
	for _, order := range permutations([]int{0, 1, 2, 3}) {
		store := ontology.NewStore()
		if err := store.CreateInstance("T1", typ, initial); err != nil {
			t.Fatal(err)
		}
		model := naive.New(typ, initial, 0)
		for _, idx := range order {
			req := writes[idx]
			got, err := store.Apply(req)
			if err != nil {
				t.Fatal(err)
			}
			want := model.Apply(req)
			if got.Outcome != want.Outcome {
				t.Fatalf("顺序 %v 请求 %s: store=%s naive=%s", order, req.RequestID, got.Outcome, want.Outcome)
			}
		}
		sv, svals, _ := store.Snapshot("T1")
		if sv != model.Version() {
			t.Fatalf("顺序 %v 版本不一致: store=%d naive=%d", order, sv, model.Version())
		}
		for _, name := range allProps {
			if !ontology.Equal(svals[name], model.Value(name)) {
				t.Fatalf("顺序 %v 属性 %s 不一致", order, name)
			}
		}
		if first {
			wantVals = svals
			first = false
		} else if !reflect.DeepEqual(svals, wantVals) {
			t.Fatalf("顺序 %v 最终值 %v 与其他顺序 %v 不同", order, svals, wantVals)
		}
	}
}

// TestVerifyDetectsTampering 重放核验必须能发现被篡改的日志。
func TestVerifyDetectsTampering(t *testing.T) {
	typ := ticketType()
	initial := ticketInitial()
	store := ontology.NewStore()
	if err := store.CreateInstance("T1", typ, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ontology.WriteRequest{
		RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{"status": "closed", "priority": int64(5)},
	}); err != nil {
		t.Fatal(err)
	}
	entries := store.Log().Entries()
	if err := ontology.Verify(typ, initial, 0, entries); err != nil {
		t.Fatalf("原始日志应通过核验: %v", err)
	}
	// 篡改判定结果。
	tampered := append([]ontology.LogEntry(nil), entries...)
	tampered[0].Outcome = ontology.OutcomeRejectedConflict
	if err := ontology.Verify(typ, initial, 0, tampered); err == nil {
		t.Fatal("篡改结果后核验应失败")
	}
	// 篡改合并证据。
	tampered2 := append([]ontology.LogEntry(nil), entries...)
	me := tampered2[0].MergeEvidence["priority"]
	me.Merged = int64(99)
	tampered2[0].MergeEvidence["priority"] = me
	if err := ontology.Verify(typ, initial, 0, tampered2); err == nil {
		t.Fatal("篡改合并证据后核验应失败")
	}
}

func permutations(items []int) [][]int {
	var out [][]int
	var rec func(prefix []int, rest []int)
	rec = func(prefix, rest []int) {
		if len(rest) == 0 {
			cp := make([]int, len(prefix))
			copy(cp, prefix)
			out = append(out, cp)
			return
		}
		for i, v := range rest {
			next := make([]int, 0, len(rest)-1)
			next = append(next, rest[:i]...)
			next = append(next, rest[i+1:]...)
			rec(append(prefix, v), next)
		}
	}
	rec(nil, items)
	return out
}
