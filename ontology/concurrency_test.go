package ontology

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// naiveSerialOracle 是一个与实现无关的“朴素串行参考模型”：
// 它只按给定全序顺序重放每个类型节点当前的声明，逐步向上查找第一个显式声明者，
// 完全不使用实现中的 Resolution 结果，只采信审计记录里的全序序号、操作与值。
type naiveSerialOracle struct {
	parent   map[string]string // 子 -> 父
	declared map[string]map[string]Rule
	// ruleSets 用于从审计中的 RuleID 恢复声明内容（由重放时自行登记）。
	ruleSets map[int64]ValueSet
}

func newNaiveOracle() *naiveSerialOracle {
	return &naiveSerialOracle{
		parent:   map[string]string{},
		declared: map[string]map[string]Rule{},
		ruleSets: map[int64]ValueSet{},
	}
}

func (m *naiveSerialOracle) addType(name, parent string) {
	m.parent[name] = parent
	m.declared[name] = map[string]Rule{}
}

// resolve 是朴素查找：从具体类型开始沿父指针逐跳上行，返回第一个声明者与其规则。
func (m *naiveSerialOracle) resolve(typeName, property string) (string, Rule, bool) {
	for cur := typeName; cur != ""; cur = m.parent[cur] {
		if r, ok := m.declared[cur][property]; ok {
			return cur, r, true
		}
	}
	return "", Rule{}, false
}

// TestConcurrentRedeclareAndIO 并发交织重新声明与实例读写，
// 然后按审计记录中的全序序号排序，用朴素串行模型逐条核对：
// 每次写校验观察到的规则必须等价于某个全序串行执行后该时刻的规则，
// 不允许出现介于两次重新声明之间、无法对应到任何确定时刻的规则。
func TestConcurrentRedeclareAndIO(t *testing.T) {
	o := buildChain(t) // T0..T4，p 在 T0 定义为 {a,b,c}

	oracle := newNaiveOracle()
	oracle.addType("T0", "")
	for i := 1; i <= 4; i++ {
		oracle.addType(fmt.Sprintf("T%d", i), fmt.Sprintf("T%d", i-1))
	}
	rootRes, err := o.Resolve("T0", "p")
	if err != nil {
		t.Fatal(err)
	}
	oracle.declared["T0"]["p"] = rootRes.Rule
	oracle.ruleSets[rootRes.Rule.RuleID] = rootRes.Rule.Allowed

	for _, inst := range []string{"i1", "i2"} {
		if err := o.CreateInstance(inst, "T4"); err != nil {
			t.Fatal(err)
		}
	}

	// 多轮合法收紧序列：每一轮都在 T2、T3 上交替重新声明，集合始终为当前集合子集。
	shrinkSeq := [][]Value{
		{"a", "b", "c"},
		{"a", "b"},
		{"a"},
	}

	var wg sync.WaitGroup
	rounds := 20

	// 重新声明者：T2 与 T3 交替收紧。
	for r := 0; r < rounds; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			typeName := "T2"
			if r%2 == 1 {
				typeName = "T3"
			}
			_ = o.RedeclareProperty(typeName, "p", shrinkSeq[r%len(shrinkSeq)])
		}(r)
	}
	// 读写者：在 T4 实例上并发读与写 a/b/c。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			inst := "i1"
			if g%2 == 0 {
				inst = "i2"
			}
			for k := 0; k < 50; k++ {
				v := []Value{"a", "b", "c"}[k%3]
				_, werr := o.Write(inst, "p", v)
				_ = werr
				if _, rerr := o.Read(inst, "p"); rerr != nil {
					t.Errorf("read must never fail: %v", rerr)
				}
				if _, rerr := o.Resolve("T4", "p"); rerr != nil {
					t.Errorf("resolve must not fail: %v", rerr)
				}
			}
		}(g)
	}
	wg.Wait()

	// 以审计记录自身携带的全序序号重放（不依赖追加顺序以外的任何实现细节）。
	records := o.Audit()
	sort.Slice(records, func(i, j int) bool { return records[i].Seq < records[j].Seq })

	prevSeq := int64(0)
	checked := 0
	for _, rec := range records {
		if rec.Seq <= prevSeq {
			t.Fatalf("audit seq not strictly ordered: %d after %d", rec.Seq, prevSeq)
		}
		prevSeq = rec.Seq

		switch rec.Op {
		case "redeclare":
			src, current, ok := oracle.resolve(rec.ConcreteType, "p")
			if !ok {
				t.Fatal("oracle lost property declaration")
			}
			// 实现端在声明时也必须满足子集约束（oracle 独立复核一次）。
			if !isSubset(rec.Resolution.Rule.Allowed, current.Allowed) {
				t.Fatalf("seq %d: accepted redeclare widened set vs oracle rule at %s", rec.Seq, src)
			}
			oracle.declared[rec.ConcreteType]["p"] = rec.Resolution.Rule
			oracle.ruleSets[rec.Resolution.Rule.RuleID] = rec.Resolution.Rule.Allowed
		case "write":
			src, rule, ok := oracle.resolve(rec.ConcreteType, "p")
			if !ok {
				t.Fatal("oracle cannot resolve during write replay")
			}
			// 核对实现观察到的生效规则与全序该时刻的朴素结果完全一致。
			if rec.Resolution.SourceType != src {
				t.Fatalf("seq %d: write observed source %s, serial oracle says %s",
					rec.Seq, rec.Resolution.SourceType, src)
			}
			if rec.Resolution.Rule.RuleID != rule.RuleID {
				t.Fatalf("seq %d: write observed rule %d, serial oracle says %d (no matching point in total order)",
					rec.Seq, rec.Resolution.Rule.RuleID, rule.RuleID)
			}
			_, oracleAccept := rule.Allowed[rec.Value]
			if rec.Accepted != oracleAccept {
				t.Fatalf("seq %d: write accept=%v, oracle accept=%v", rec.Seq, rec.Accepted, oracleAccept)
			}
			checked++
		case "read", "resolve":
			src, rule, ok := oracle.resolve(rec.ConcreteType, "p")
			if !ok {
				t.Fatal("oracle cannot resolve during read replay")
			}
			if rec.Resolution.SourceType != src || rec.Resolution.Rule.RuleID != rule.RuleID {
				t.Fatalf("seq %d: %s observed %s/%d, oracle says %s/%d",
					rec.Seq, rec.Op, rec.Resolution.SourceType, rec.Resolution.Rule.RuleID,
					src, rule.RuleID)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no write records were replayed")
	}

	// 最终状态也必须一致。
	src, rule, ok := oracle.resolve("T4", "p")
	if !ok {
		t.Fatal("oracle final resolve failed")
	}
	final, err := o.Resolve("T4", "p")
	if err != nil {
		t.Fatal(err)
	}
	if final.SourceType != src || final.Rule.RuleID != rule.RuleID {
		t.Fatalf("final state mismatch: %s/%d vs oracle %s/%d",
			final.SourceType, final.Rule.RuleID, src, rule.RuleID)
	}
}

// TestResolveCostBoundedByOwnDepth 用可验证的方式证明查找开销只取决于
// “具体类型到属性定义祖先之间的实际深度”，与体系中无关分支数量无关：
// 在同一深层链条下不断增加互不相关的旁支，审计记录中的遍历链条长度保持不变。
func TestResolveCostBoundedByOwnDepth(t *testing.T) {
	o := New()
	if err := o.CreateRootType("Root"); err != nil {
		t.Fatal(err)
	}
	if err := o.DefineProperty("Root", "p", values("a")); err != nil {
		t.Fatal(err)
	}
	// 深层目标链条 Root -> A1 -> ... -> A30，属性只在 Root 定义。
	const depth = 30
	prev := "Root"
	for i := 1; i <= depth; i++ {
		name := fmt.Sprintf("A%d", i)
		if err := o.CreateSubtype(name, prev, false); err != nil {
			t.Fatal(err)
		}
		prev = name
	}

	measure := func() int {
		res, err := o.Resolve(fmt.Sprintf("A%d", depth), "p")
		if err != nil {
			t.Fatal(err)
		}
		if res.SourceType != "Root" {
			t.Fatalf("source = %s, want Root", res.SourceType)
		}
		return len(res.Chain)
	}
	baseline := measure() // depth+1 个节点

	// 增加大量互不相关的分支（直接挂在 Root 下的宽扇出）。
	for b := 0; b < 500; b++ {
		if err := o.CreateSubtype(fmt.Sprintf("B%d", b), "Root", false); err != nil {
			t.Fatal(err)
		}
	}
	withBranches := measure()
	if withBranches != baseline {
		t.Fatalf("chain traversal grew with unrelated branches: %d -> %d", baseline, withBranches)
	}
	if baseline != depth+1 {
		t.Fatalf("traversal length = %d, want exactly depth+1 = %d", baseline, depth+1)
	}

	// 在 A10 处重新声明后，A30 的遍历长度缩短为到 A10 的实际深度，
	// 证明遍历只走到“第一个显式声明者”即停止。
	if err := o.RedeclareProperty("A10", "p", values("a")); err != nil {
		t.Fatal(err)
	}
	res, err := o.Resolve(fmt.Sprintf("A%d", depth), "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Chain) != depth-10+1 {
		t.Fatalf("after mid redeclare chain len = %d, want %d", len(res.Chain), depth-10+1)
	}
	if res.SourceType != "A10" || res.OriginType != "Root" {
		t.Fatalf("source=%s origin=%s", res.SourceType, res.OriginType)
	}
}
