package ontology

import (
	"fmt"
	"testing"
)

const costSpec LinkTypeName = "many"

// TestValidationCostIndependentOfTotalLinks 以「基础映射访问次数」作为可审计的
// 工作量单位，证明对单条待创建链接的基数校验开销不随该类型链接总数增长：
// 从 0 到 4096 条链接，CheckCost 恒为同一常数（两端均有限 => 6 次映射访问）。
func TestValidationCostIndependentOfTotalLinks(t *testing.T) {
	sizes := []int{0, 512, 2048, 4096}
	var base int
	for _, n := range sizes {
		l, spec := newFilledLedger(t, n)
		cost, err := l.CheckCost(costSpec)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			base = cost
			t.Logf("cost at %5d links = %d (type+2 instances+dedup+2 counts)", n, cost)
		} else {
			t.Logf("cost at %5d links = %d (unchanged); naive recount must scan all %d links",
				n, cost, n)
		}
		if cost != base {
			t.Fatalf("cost grew with dataset size: n=%d cost=%d base=%d", n, cost, base)
		}

		// 对照组：朴素模型要得到「是否超限」的结论必须遍历全量链接，工作量为 n。
		naive := newNaiveFromLedger(l, spec)
		if work := len(naive.links); work != n {
			t.Fatalf("naive model scan work=%d want %d", work, n)
		}
	}
}

func newFilledLedger(t *testing.T, n int) (*Ledger, LinkTypeSpec) {
	t.Helper()
	l := NewLedger()
	spec := LinkTypeSpec{
		Name:        costSpec,
		SourceType:  "S",
		TargetType:  "T",
		SourceBound: AtMost(n + 1),
		TargetBound: AtMost(n + 1),
	}
	must(t, l.RegisterLinkType(spec))
	for i := 0; i < n; i++ {
		sid := InstanceID(fmt.Sprintf("s%05d", i))
		tid := InstanceID(fmt.Sprintf("t%05d", i))
		must(t, l.CreateObject("S", sid))
		must(t, l.CreateObject("T", tid))
		must(t, l.CreateLink(costSpec, sid, tid))
	}
	if l.LinkCount() != n {
		t.Fatalf("fill failed: links=%d want %d", l.LinkCount(), n)
	}
	return l, spec
}

func newNaiveFromLedger(l *Ledger, spec LinkTypeSpec) *naiveModel {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := &naiveModel{
		spec:         spec,
		links:        map[naiveLink]struct{}{},
		sourceExists: map[InstanceID]struct{}{},
		targetExists: map[InstanceID]struct{}{},
	}
	for id := range l.objects[spec.SourceType] {
		m.sourceExists[id] = struct{}{}
	}
	for id := range l.objects[spec.TargetType] {
		m.targetExists[id] = struct{}{}
	}
	for k := range l.links {
		m.links[naiveLink{typ: k.Type, source: k.Source, target: k.Target}] = struct{}{}
	}
	return m
}

// BenchmarkCreateDeleteAtFixedOccupancy 在 10000 条链接的账本上对同一有序对
// 反复创建/删除；单实例占用恒定为 1。若校验与全局规模无关，ns/op 近似恒定。
func BenchmarkCreateDeleteAtFixedOccupancy(b *testing.B) {
	l := NewLedger()
	if err := l.RegisterLinkType(LinkTypeSpec{
		Name:        "b",
		SourceType:  "S",
		TargetType:  "T",
		SourceBound: AtMost(1_000_000),
		TargetBound: AtMost(1_000_000),
	}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		sid := InstanceID(fmt.Sprintf("s%06d", i))
		tid := InstanceID(fmt.Sprintf("t%06d", i))
		_ = l.CreateObject("S", sid)
		_ = l.CreateObject("T", tid)
		_ = l.CreateLink("b", sid, tid)
	}
	_ = l.CreateObject("S", "probe-s")
	_ = l.CreateObject("T", "probe-t")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.CreateLink("b", "probe-s", "probe-t"); err != nil {
			b.Fatal(err)
		}
		if err := l.DeleteLink("b", "probe-s", "probe-t"); err != nil {
			b.Fatal(err)
		}
	}
}
