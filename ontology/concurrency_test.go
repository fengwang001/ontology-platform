package ontology

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

var errChaos = errors.New("chaos hook failure")

// chaosBehavior 是确定性钩子行为：只取决于钩子标识与校验目标，
// 因此朴素串行模型可以独立重放并得到相同结论。
func chaosBehavior(id string, target Target) (Decision, error) {
	h := fnv.New32a()
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(target.Kind))
	h.Write([]byte{0})
	h.Write([]byte(target.Name))
	switch h.Sum32() % 23 {
	case 0:
		return Allow, errChaos
	case 1, 2, 3:
		return Reject, nil
	default:
		return Allow, nil
	}
}

func chaosHook(id string) Hook {
	return HookFunc(func(_ context.Context, target Target) (Decision, error) {
		return chaosBehavior(id, target)
	})
}

// ---------------------------------------------------------------------------
// 朴素串行参照实现：只按版本号顺序应用变更，独立重放校验语义。
// 并发测试用它对照真实注册表在并发交织下观察到的快照与结论。
// ---------------------------------------------------------------------------

type naiveModel struct {
	specs map[string]GroupSpec
	hooks map[string][]string // group -> 按注册顺序的钩子标识
}

func newNaiveModel(specs []GroupSpec) *naiveModel {
	m := &naiveModel{specs: make(map[string]GroupSpec), hooks: make(map[string][]string)}
	for _, s := range specs {
		m.specs[s.Name] = s
	}
	return m
}

func (m *naiveModel) register(group, id string) {
	m.hooks[group] = append(m.hooks[group], id)
}

func (m *naiveModel) unregister(group, id string) {
	ids := m.hooks[group]
	for i, hid := range ids {
		if hid == id {
			m.hooks[group] = append(ids[:i], ids[i+1:]...)
			return
		}
	}
}

func (m *naiveModel) sortedGroups() []string {
	names := make([]string, 0, len(m.hooks))
	for name, ids := range m.hooks {
		if len(ids) > 0 {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := m.specs[names[i]], m.specs[names[j]]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.Name < b.Name
	})
	return names
}

type expectedHook struct {
	id     string
	status HookStatus
}

type expectedGroup struct {
	name    string
	outcome Outcome
	hooks   []expectedHook
}

type expectedReport struct {
	decision Decision
	groups   []expectedGroup
	hasError bool
}

// loggedOp 是一次成功变更的版本化记录，用于事后按全序回放。
type loggedOp struct {
	version uint64
	regs    []HookRegistration // 单钩子注册视为长度为 1 的批次
	unreg   *HookRegistration  // 注销时仅使用 Group 与 ID 字段
}

type loggedValidate struct {
	target Target
	rep    Report
	err    error
}

var chaosGroups = []GroupSpec{
	{Name: "g0-sc", Priority: 0, ShortCircuit: true},
	{Name: "g1-agg", Priority: 1, ShortCircuit: false},
	{Name: "g2-sc", Priority: 2, ShortCircuit: true},
	{Name: "g3-agg", Priority: 3, ShortCircuit: false},
}

var chaosTargets = []Target{
	{Kind: "objectType", Name: "Employee"},
	{Kind: "objectType", Name: "Company"},
	{Kind: "linkType", Name: "WorksFor"},
	{Kind: "linkType", Name: "Owns"},
}

// TestConcurrentLinearizability 并发交织注册、注销、批量注册与校验调用，
// 然后把全部变更按版本号串行回放到朴素模型，
// 校验每次调用观察到的快照内容与最终结论都与该全序下的某一时刻一致。
func TestConcurrentLinearizability(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, chaosGroups...)

	var mu sync.Mutex
	var ops []loggedOp
	var vals []loggedValidate

	const workers = 8
	const opsPerWorker = 250

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + w)))
			live := make(map[string]string) // id -> group，仅本 worker 注册的钩子
			seq := 0
			nextID := func() string {
				seq++
				return fmt.Sprintf("w%02d-h%04d", w, seq)
			}
			for i := 0; i < opsPerWorker; i++ {
				switch rng.Intn(10) {
				case 0, 1, 2: // 单钩子注册
					id := nextID()
					group := chaosGroups[rng.Intn(len(chaosGroups))].Name
					reg := HookRegistration{Group: group, ID: id, Hook: chaosHook(id)}
					v, err := r.Register(reg)
					if err != nil {
						t.Errorf("Register(%s/%s): %v", group, id, err)
						return
					}
					live[id] = group
					mu.Lock()
					ops = append(ops, loggedOp{version: v, regs: []HookRegistration{reg}})
					mu.Unlock()
				case 3: // 批量注册：批次内相对顺序必须保留
					group := chaosGroups[rng.Intn(len(chaosGroups))].Name
					n := 2 + rng.Intn(3)
					regs := make([]HookRegistration, 0, n)
					for k := 0; k < n; k++ {
						id := nextID()
						regs = append(regs, HookRegistration{Group: group, ID: id, Hook: chaosHook(id)})
						live[id] = group
					}
					v, err := r.RegisterBatch(regs)
					if err != nil {
						t.Errorf("RegisterBatch: %v", err)
						return
					}
					mu.Lock()
					ops = append(ops, loggedOp{version: v, regs: regs})
					mu.Unlock()
				case 4, 5: // 注销本 worker 的存活钩子
					if len(live) == 0 {
						continue
					}
					ids := make([]string, 0, len(live))
					for id := range live {
						ids = append(ids, id)
					}
					id := ids[rng.Intn(len(ids))]
					group := live[id]
					v, err := r.Unregister(group, id)
					if err != nil {
						t.Errorf("Unregister(%s/%s): %v", group, id, err)
						return
					}
					delete(live, id)
					mu.Lock()
					ops = append(ops, loggedOp{version: v, unreg: &HookRegistration{Group: group, ID: id}})
					mu.Unlock()
				default: // 校验调用
					target := chaosTargets[rng.Intn(len(chaosTargets))]
					rep, err := r.Validate(context.Background(), target)
					mu.Lock()
					vals = append(vals, loggedValidate{target: target, rep: rep, err: err})
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	if len(vals) == 0 {
		t.Fatal("no validate calls recorded")
	}

	// 版本号由同一把互斥锁分配，天然给出全序；按版本串行回放。
	sort.Slice(ops, func(i, j int) bool { return ops[i].version < ops[j].version })
	for i := 1; i < len(ops); i++ {
		if ops[i].version == ops[i-1].version {
			t.Fatalf("duplicate version %d: total order violated", ops[i].version)
		}
	}

	model := newNaiveModel(chaosGroups)
	opIdx := 0
	advance := func(version uint64) {
		for opIdx < len(ops) && ops[opIdx].version <= version {
			op := ops[opIdx]
			if op.unreg != nil {
				model.unregister(op.unreg.Group, op.unreg.ID)
			} else {
				for _, reg := range op.regs {
					model.register(reg.Group, reg.ID)
				}
			}
			opIdx++
		}
	}

	sort.Slice(vals, func(i, j int) bool { return vals[i].rep.SnapshotVersion < vals[j].rep.SnapshotVersion })
	for _, val := range vals {
		advance(val.rep.SnapshotVersion)
		want := model.execute(val.target)

		if gotErr := val.err != nil; gotErr != want.hasError {
			t.Fatalf("version %d target %+v: error = %v, want hasError = %v",
				val.rep.SnapshotVersion, val.target, val.err, want.hasError)
		}
		if !want.hasError && val.rep.Decision != want.decision {
			t.Fatalf("version %d target %+v: decision = %v, want %v",
				val.rep.SnapshotVersion, val.target, val.rep.Decision, want.decision)
		}
		if len(val.rep.Groups) != len(want.groups) {
			t.Fatalf("version %d target %+v: %d groups, want %d",
				val.rep.SnapshotVersion, val.target, len(val.rep.Groups), len(want.groups))
		}
		for gi, wg := range want.groups {
			gg := val.rep.Groups[gi]
			if gg.Group.Name != wg.name {
				t.Fatalf("version %d: group[%d] = %s, want %s", val.rep.SnapshotVersion, gi, gg.Group.Name, wg.name)
			}
			if gg.Outcome != wg.outcome {
				t.Fatalf("version %d target %+v group %s: outcome = %s, want %s",
					val.rep.SnapshotVersion, val.target, wg.name, gg.Outcome, wg.outcome)
			}
			if len(gg.Hooks) != len(wg.hooks) {
				t.Fatalf("version %d group %s: %d hooks, want %d",
					val.rep.SnapshotVersion, wg.name, len(gg.Hooks), len(wg.hooks))
			}
			for hi, wh := range wg.hooks {
				gh := gg.Hooks[hi]
				if gh.ID != wh.id || gh.Status != wh.status {
					t.Fatalf("version %d group %s hook[%d] = (%s,%s), want (%s,%s)",
						val.rep.SnapshotVersion, wg.name, hi, gh.ID, gh.Status, wh.id, wh.status)
				}
			}
		}
	}
	t.Logf("checked %d mutations and %d validate calls against naive serial model", len(ops), len(vals))
}

// TestConcurrentBatchAtomicity 并发提交多批次注册：
// 每个批次对外是单次动作，批次内钩子必须在组内顺序中连续且保持声明顺序。
func TestConcurrentBatchAtomicity(t *testing.T) {
	r := NewRegistry()
	mustDeclare(t, r, GroupSpec{Name: "g", Priority: 0})

	const batches = 16
	const batchSize = 5

	var wg sync.WaitGroup
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			regs := make([]HookRegistration, 0, batchSize)
			for k := 0; k < batchSize; k++ {
				id := fmt.Sprintf("b%02d-%d", b, k)
				regs = append(regs, HookRegistration{Group: "g", ID: id, Hook: allowAll()})
			}
			if _, err := r.RegisterBatch(regs); err != nil {
				t.Errorf("RegisterBatch: %v", err)
			}
		}(b)
	}
	wg.Wait()

	ids := snapshotIDs(r.Snapshot())["g"]
	if len(ids) != batches*batchSize {
		t.Fatalf("hooks = %d, want %d", len(ids), batches*batchSize)
	}
	// 扫描快照：每个批次一旦出现，必须连续且按声明顺序完整出现。
	seen := make(map[string]bool)
	for i := 0; i < len(ids); {
		prefix := ids[i][:len(ids[i])-1] // "b%02d-"
		if seen[prefix] {
			t.Fatalf("batch %s interleaved with other registrations at index %d", prefix, i)
		}
		seen[prefix] = true
		for k := 0; k < batchSize; k++ {
			want := fmt.Sprintf("%s%d", prefix, k)
			if i+k >= len(ids) || ids[i+k] != want {
				t.Fatalf("batch %s broken at index %d: got %v around it", prefix, i, ids[max(0, i-1):min(len(ids), i+batchSize+1)])
			}
		}
		i += batchSize
	}
}

// execute 独立重放一次校验，不依赖 Registry 的执行代码。
func (m *naiveModel) execute(target Target) expectedReport {
	rep := expectedReport{decision: Allow}
	halted := false
	for _, name := range m.sortedGroups() {
		spec := m.specs[name]
		ids := m.hooks[name]
		eg := expectedGroup{name: name, outcome: OutcomePassed}
		if halted {
			eg.outcome = OutcomeNotExecuted
			for _, id := range ids {
				eg.hooks = append(eg.hooks, expectedHook{id, HookNotExecuted})
			}
			rep.groups = append(rep.groups, eg)
			continue
		}
		rejected := false
		for i, id := range ids {
			if rejected && spec.ShortCircuit {
				eg.hooks = append(eg.hooks, expectedHook{id, HookSkipped})
				continue
			}
			d, err := chaosBehavior(id, target)
			if err != nil {
				eg.hooks = append(eg.hooks, expectedHook{id, HookErrored})
				for _, rest := range ids[i+1:] {
					eg.hooks = append(eg.hooks, expectedHook{rest, HookSkipped})
				}
				eg.outcome = OutcomeIndeterminate
				rep.groups = append(rep.groups, eg)
				rep.hasError = true
				for _, lower := range m.sortedGroups()[len(rep.groups):] {
					lg := expectedGroup{name: lower, outcome: OutcomeNotExecuted}
					for _, lid := range m.hooks[lower] {
						lg.hooks = append(lg.hooks, expectedHook{lid, HookNotExecuted})
					}
					rep.groups = append(rep.groups, lg)
				}
				return rep
			}
			if d == Reject {
				eg.hooks = append(eg.hooks, expectedHook{id, HookRejected})
				rejected = true
			} else {
				eg.hooks = append(eg.hooks, expectedHook{id, HookPassed})
			}
		}
		if rejected {
			if spec.ShortCircuit {
				eg.outcome = OutcomeRejectedShortCircuit
			} else {
				eg.outcome = OutcomeRejectedAggregate
			}
			rep.decision = Reject
			halted = true
		}
		rep.groups = append(rep.groups, eg)
	}
	return rep
}
