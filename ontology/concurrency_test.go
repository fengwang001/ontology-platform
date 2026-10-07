package ontology

import (
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveModel 是测试内的朴素串行实现：无锁、直接沿父链查找，
// 用于与并发执行后的 Registry 状态逐条对照。
type naiveModel struct {
	parent   map[string]string
	declared map[string]map[string]Rule
	values   map[string]map[string]string // instance -> property -> value
	instType map[string]string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		parent:   make(map[string]string),
		declared: make(map[string]map[string]Rule),
		values:   make(map[string]map[string]string),
		instType: make(map[string]string),
	}
}

func (m *naiveModel) effective(typeID, prop string) (string, Rule, bool) {
	for cur := typeID; cur != ""; cur = m.parent[cur] {
		if rule, ok := m.declared[cur][prop]; ok {
			return cur, rule, true
		}
	}
	return "", Rule{}, false
}

// TestConcurrentRedeclareAndAccessLinearizable 并发交织重新声明与实例读写，
// 然后按全局序号在朴素串行模型上重放全部已接受变更与查找审计，
// 验证每次读写观察到的生效规则都对应某个确定的串行时刻。
func TestConcurrentRedeclareAndAccessLinearizable(t *testing.T) {
	r := NewRegistry()
	mustCreateChain(t, r, "R", "M", "L")
	universe := []string{"v0", "v1", "v2", "v3", "v4", "v5", "v6", "v7"}
	if err := r.DeclareRule("R", "p", NewRule(universe)); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateInstance("inst", "L"); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const opsPerWorker = 300
	types := []string{"R", "M", "L"}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerWorker; i++ {
				switch rng.Intn(3) {
				case 0: // 重新声明：取目标类型当前生效规则的随机子集，保证声明合法
					target := types[rng.Intn(len(types))]
					cur, err := r.EffectiveRule(target, "p")
					if err != nil {
						continue
					}
					var subset []string
					for _, v := range cur.Rule.Values() {
						if rng.Intn(2) == 0 {
							subset = append(subset, v)
						}
					}
					// 与并发收窄竞争失败（ErrRuleWidening）是合法结果，直接跳过。
					_ = r.DeclareRule(target, "p", NewRule(subset))
				case 1: // 写入：可能因规则收窄被拒绝，属合法结果
					_ = r.WriteInstance("inst", "p", universe[rng.Intn(len(universe))])
				default: // 读取：生效规则查找 + 历史值读取
					if _, err := r.EffectiveRule("L", "p"); err != nil {
						t.Errorf("lookup must succeed: %v", err)
					}
					if _, _, err := r.ReadInstance("inst", "p"); err != nil {
						t.Errorf("read must succeed: %v", err)
					}
				}
			}
		}(int64(w*1000 + 42))
	}
	wg.Wait()

	// 按全局序号合并变更与查找审计，在朴素模型上串行重放。
	type event struct {
		seq uint64
		mut *MutationRecord
		lk  *LookupRecord
	}
	var events []event
	for _, m := range r.MutationLog() {
		m := m
		events = append(events, event{seq: m.Seq, mut: &m})
	}
	for _, a := range r.AuditLog() {
		a := a
		events = append(events, event{seq: a.Seq, lk: &a})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].seq < events[j].seq })

	model := newNaiveModel()
	model.parent["M"] = "R"
	model.parent["L"] = "M"
	model.declared["R"] = map[string]Rule{"p": NewRule(universe)}
	model.instType["inst"] = "L"
	model.values["inst"] = make(map[string]string)

	for _, ev := range events {
		switch {
		case ev.mut != nil && ev.mut.Kind == MutationDeclareRule:
			m := ev.mut
			_, cur, _ := model.effective(m.TypeID, m.Property)
			if !m.Rule.IsSubsetOf(cur) {
				t.Fatalf("seq %d: accepted redeclaration violates subset constraint against serial model", m.Seq)
			}
			if model.declared[m.TypeID] == nil {
				model.declared[m.TypeID] = make(map[string]Rule)
			}
			model.declared[m.TypeID][m.Property] = m.Rule
		case ev.mut != nil && ev.mut.Kind == MutationWriteValue:
			m := ev.mut
			_, cur, _ := model.effective(m.TypeID, m.Property)
			if !cur.Equal(m.RuleUsed) {
				t.Fatalf("seq %d: write validated against rule %v, serial model says %v",
					m.Seq, m.RuleUsed.Values(), cur.Values())
			}
			if !cur.Allows(m.Value) {
				t.Fatalf("seq %d: accepted write value %q not allowed by rule in use", m.Seq, m.Value)
			}
			model.values[m.Instance][m.Property] = m.Value
		case ev.lk != nil:
			lk := ev.lk
			src, cur, _ := model.effective(lk.TypeID, lk.Property)
			if src != lk.SourceType || !cur.Equal(lk.Rule) {
				t.Fatalf("seq %d: lookup observed (%q, %v), serial model says (%q, %v)",
					lk.Seq, lk.SourceType, lk.Rule.Values(), src, cur.Values())
			}
		}
	}

	// 并发阶段结束后的最终生效规则必须与朴素串行模型一致。
	final, err := r.EffectiveRule("L", "p")
	if err != nil {
		t.Fatal(err)
	}
	src, cur, _ := model.effective("L", "p")
	if final.SourceType != src || !final.Rule.Equal(cur) {
		t.Fatalf("final effective rule mismatch: registry (%q, %v), model (%q, %v)",
			final.SourceType, final.Rule.Values(), src, cur.Values())
	}

	// 实例最终保存值与朴素模型一致。
	v, written, err := r.ReadInstance("inst", "p")
	if err != nil {
		t.Fatal(err)
	}
	mv, mwritten := model.values["inst"]["p"]
	if written != mwritten || v != mv {
		t.Fatalf("final instance value mismatch: registry (%q,%v), model (%q,%v)", v, written, mv, mwritten)
	}
}
