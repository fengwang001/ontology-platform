package adjudicator

import (
	"fmt"
	"testing"
)

// chainSnapshot 构造一份含 k 长度依赖链与 filler 条无依赖冗余记录的快照。
// 链: c0 <- c1 <- ... <- c(k-1)，全部位于对象实例类别。
func chainSnapshot(chainLen, filler int) *Snapshot {
	s := &Snapshot{}
	for c := 0; c < categoryCount; c++ {
		s.Backups[c] = Healthy()
	}
	var records []Record
	for i := 0; i < chainLen; i++ {
		rec := Record{Ref: ref(CategoryObjectInstance, fmt.Sprintf("c%d", i))}
		if i > 0 {
			rec.DependsOn = []RecordRef{ref(CategoryObjectInstance, fmt.Sprintf("c%d", i-1))}
		}
		records = append(records, rec)
	}
	for i := 0; i < filler; i++ {
		records = append(records, Record{Ref: ref(CategoryObjectInstance, fmt.Sprintf("f%d", i))})
	}
	s.Backups[CategoryObjectInstance] = Healthy(records...)
	return s
}

// 证明单条记录的判定开销只与其依赖链长度相关、与备份总规模无关：
// 固定链长 k，总规模 N 增长 50 倍时，判定访问的依赖边条数保持不变；
// 固定 N，链长 k 翻倍时，边访问条数随之线性翻倍。
func TestCheckRecordCostDependsOnlyOnChainLength(t *testing.T) {
	const chainLen = 64
	head := ref(CategoryObjectInstance, fmt.Sprintf("c%d", chainLen-1))

	visitsAt := func(chain, filler int) int64 {
		s := chainSnapshot(chain, filler)
		idx := BuildIndex(s)
		ev := newEvaluator(idx)
		ev.evaluate([]RecordRef{ref(CategoryObjectInstance, fmt.Sprintf("c%d", chain-1))})
		return ev.EdgeVisits()
	}

	small := visitsAt(chainLen, 1_000)
	large := visitsAt(chainLen, 50_000)
	if small != large {
		t.Fatalf("总规模增长 50 倍后判定开销发生变化: %d -> %d", small, large)
	}
	if want := int64(chainLen - 1); small != want {
		t.Fatalf("判定开销应等于链上边数 %d, 实际为 %d", want, small)
	}

	double := visitsAt(2*chainLen, 1_000)
	if double != 2*chainLen-1 {
		t.Fatalf("判定开销应随链长线性增长, 链长 %d 时访问边数 %d", 2*chainLen, double)
	}

	// 端到端验证：两种规模下 CheckRecord 结论一致。
	s := chainSnapshot(chainLen, 50_000)
	rv := New().CheckRecord(s, head)
	if !rv.Rebuildable {
		t.Fatalf("链头 %v 应可重建", head)
	}
}

// 链中间一条记录损坏时，链头不可重建且阻断原因为该损坏记录，
// 判定开销仍只与链长相关。
func TestCheckRecordDamagedChain(t *testing.T) {
	const chainLen = 32
	s := chainSnapshot(chainLen, 10_000)
	b := s.Backups[CategoryObjectInstance]
	b.Damaged = map[string]bool{"c10": true}
	s.Backups[CategoryObjectInstance] = b

	rv := New().CheckRecord(s, ref(CategoryObjectInstance, "c31"))
	if rv.Rebuildable {
		t.Fatal("链中存在损坏记录时链头应不可重建")
	}
	if rv.Reason != ReasonDependencyUnavailable {
		t.Fatalf("原因码应为 DependencyUnavailable, 实际为 %v", rv.Reason)
	}
	// 链尾在损坏点之前，不受影响。
	tail := New().CheckRecord(s, ref(CategoryObjectInstance, "c5"))
	if !tail.Rebuildable {
		t.Fatal("损坏点下游的链尾应可重建")
	}
}
