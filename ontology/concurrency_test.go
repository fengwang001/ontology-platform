package ontology

import (
	"math/rand"
	"sync"
	"testing"
)

// concOp 是并发线性化测试中的一个调用。
type concOp struct {
	kind     string // "set-default" | "set-override" | "revoke" | "decide"
	tenant   string
	typeName string
	entries  []RuleEntry
	subject  Subject
	action   Action
	instID   string
	attr     string
}

type concResult struct {
	class ErrorClass
	isNil bool
	dec   Decision
}

func runOp(e engineAPI, op concOp) concResult {
	var err error
	var dec Decision
	switch op.kind {
	case "set-default":
		err = e.SetGlobalDefault(op.typeName, op.entries)
	case "set-override":
		err = e.SetTenantOverride(op.tenant, op.typeName, op.entries)
	case "revoke":
		err = e.RevokeTenantOverride(op.tenant, op.typeName)
	case "decide":
		dec, err = e.Decide(op.subject, op.action, op.instID, op.attr)
	}
	return concResult{class: ClassOf(err), isNil: err == nil, dec: dec}
}

func sameResult(a, b concResult) bool {
	if a.isNil != b.isNil || a.class != b.class {
		return false
	}
	if a.class != 0 {
		return true // 同类错误视为等价（消息文本不参与线性化判定）
	}
	return decisionsEqual(a.dec, b.dec)
}

func decisionsEqual(a, b Decision) bool {
	if a.Effect != b.Effect ||
		a.Basis.OwnerTenant != b.Basis.OwnerTenant ||
		a.Basis.DefaultVersion != b.Basis.DefaultVersion ||
		a.Basis.OverrideVersion != b.Basis.OverrideVersion {
		return false
	}
	return winningKeys(a) == winningKeys(b)
}

func winningKeys(d Decision) string {
	keys := ""
	for _, w := range d.Basis.WinningEntries {
		keys += w.Layer.String() + "|" + string(w.Entry.Action) + "|" + w.Entry.Role + "|" +
			w.Entry.Attribute + "|" + w.Entry.Predicate + "|" + w.Entry.Effect.String() + ";"
	}
	return keys
}

// TestConcurrentLinearizable 对每一轮并发调用，暴力枚举所有串行顺序，
// 验证存在某个串行执行顺序使参照实现产出与并发执行完全一致的结果。
func TestConcurrentLinearizable(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const rounds = 40
	const nOps = 6

	for round := 0; round < rounds; round++ {
		main := NewEngine()
		instances := setupDiff(t, main)
		// 预置一些规则，让判定更有内容。
		presets := map[string][]RuleEntry{"doc": randEntries(r), "open": randEntries(r)}
		for typeName, entries := range presets {
			// 预置可能因声明内冲突而被拒；主实现与参照重放使用相同条目，
			// 两侧行为一致，因此这里忽略错误。
			_ = main.SetGlobalDefault(typeName, entries)
		}

		ops := make([]concOp, nOps)
		for i := range ops {
			ops[i] = randConcOp(r, instances)
		}

		// 并发执行：所有 goroutine 在同一屏障后启动。
		results := make([]concResult, nOps)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range ops {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i] = runOp(main, ops[i])
			}(i)
		}
		close(start)
		wg.Wait()

		// 枚举全部串行顺序，在参照实现上重放（含相同预置）。
		if !existsSerialOrder(ops, results, instances, presets) {
			t.Fatalf("round %d: no serial order explains concurrent results\nops=%+v\npresets=%+v\nresults=%+v",
				round, ops, presets, results)
		}
	}
}

func randConcOp(r *rand.Rand, instances []Instance) concOp {
	tenant := pickString(r, diffTenants)
	typeName := pickString(r, diffTypes)
	switch r.Intn(4) {
	case 0:
		return concOp{kind: "set-default", typeName: typeName, entries: randEntries(r)}
	case 1:
		return concOp{kind: "set-override", tenant: tenant, typeName: typeName, entries: randEntries(r)}
	case 2:
		return concOp{kind: "revoke", tenant: tenant, typeName: typeName}
	default:
		inst := instances[r.Intn(len(instances))]
		return concOp{
			kind:    "decide",
			subject: randSubject(r),
			action:  diffActions[r.Intn(len(diffActions))],
			instID:  inst.ID,
			attr:    pickString(r, diffAttributes),
		}
	}
}

func existsSerialOrder(ops []concOp, results []concResult, instances []Instance, presets map[string][]RuleEntry) bool {
	perm := make([]int, len(ops))
	for i := range perm {
		perm[i] = i
	}
	var found bool
	permute(perm, 0, func() {
		if found {
			return
		}
		ref := NewReferenceEngine()
		for _, tenant := range diffTenants {
			if err := ref.RegisterTenant(tenant); err != nil {
				return
			}
		}
		for _, def := range diffTypeDefs() {
			if err := ref.RegisterObjectType(def); err != nil {
				return
			}
		}
		for _, inst := range instances {
			if err := ref.RegisterInstance(inst); err != nil {
				return
			}
		}
		for _, typeName := range []string{"doc", "open"} {
			// 与主实现一致：预置被拒（声明内冲突）时忽略错误继续。
			_ = ref.SetGlobalDefault(typeName, presets[typeName])
		}
		ok := true
		for _, idx := range perm {
			got := runOp(ref, ops[idx])
			if !sameResult(got, results[idx]) {
				ok = false
				break
			}
		}
		if ok {
			found = true
		}
	})
	return found
}

func permute(perm []int, i int, visit func()) {
	if i == len(perm) {
		visit()
		return
	}
	for j := i; j < len(perm); j++ {
		perm[i], perm[j] = perm[j], perm[i]
		permute(perm, i+1, visit)
		perm[i], perm[j] = perm[j], perm[i]
	}
}

// TestConcurrentDefaultSwitchMonotone 全局默认规则并发变更时，
// 审计日志中判定所引用的默认版本必须单调不减（原子切换，无中间窗口）。
func TestConcurrentDefaultSwitchMonotone(t *testing.T) {
	e := NewEngine()
	setupDiff(t, e)
	v1 := []RuleEntry{{Action: "read", Attribute: "title", Effect: Allow}}
	v2 := []RuleEntry{{Action: "read", Attribute: "title", Effect: Deny}}
	if err := e.SetGlobalDefault("doc", v1); err != nil {
		t.Fatalf("set default: %v", err)
	}

	const switches = 30
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subj := Subject{ID: "u", Tenant: "t1"}
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := e.Decide(subj, "read", "t1-doc", "title"); err != nil {
						t.Errorf("decide: %v", err)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < switches; i++ {
		entries := v1
		if i%2 == 1 {
			entries = v2
		}
		if err := e.SetGlobalDefault("doc", entries); err != nil {
			t.Fatalf("switch default: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	var lastVersion uint64
	for _, rec := range e.Audit() {
		if rec.Op != OpDecide {
			continue
		}
		d := rec.Output.(Decision)
		if d.Basis.DefaultVersion < lastVersion {
			t.Fatalf("default version went backwards in audit order: %d after %d", d.Basis.DefaultVersion, lastVersion)
		}
		lastVersion = d.Basis.DefaultVersion
	}
}

// TestConcurrentRevokeAtomicity 并发撤销与判定时，每次判定要么整体看到
// 覆盖规则（版本 1，拒绝），要么整体看到默认规则（版本 0，允许），
// 不允许出现部分属性沿用覆盖、部分属性恢复默认的中间状态。
func TestConcurrentRevokeAtomicity(t *testing.T) {
	for round := 0; round < 20; round++ {
		e := NewEngine()
		setupDiff(t, e)
		if err := e.SetGlobalDefault("open", []RuleEntry{{Action: "read", Effect: Allow}}); err != nil {
			t.Fatalf("set default: %v", err)
		}
		if err := e.SetTenantOverride("t0", "open", []RuleEntry{
			{Action: "read", Attribute: "title", Effect: Deny},
			{Action: "read", Attribute: "secret", Effect: Deny},
		}); err != nil {
			t.Fatalf("set override: %v", err)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan Decision, 64)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(attr string) {
				defer wg.Done()
				<-start
				d, err := e.Decide(Subject{Tenant: "t3"}, "read", "t0-open", attr)
				if err != nil {
					t.Errorf("decide: %v", err)
					return
				}
				results <- d
			}(diffAttributes[i%2])
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := e.RevokeTenantOverride("t0", "open"); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}()
		close(start)
		wg.Wait()
		close(results)

		for d := range results {
			withOverride := d.Basis.OverrideVersion == 1 && d.Effect == Deny
			withoutOverride := d.Basis.OverrideVersion == 0 && d.Effect == Allow
			if !withOverride && !withoutOverride {
				t.Fatalf("round %d: decision in intermediate state: %+v", round, d)
			}
		}
	}
}
