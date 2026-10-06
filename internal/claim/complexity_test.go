package claim_test

import (
	"fmt"
	"testing"

	"ontology/internal/claim"
)

// 登记开销只与覆盖保单数相关、不随全部事故总数增长。
//
// 可验证方式：内部统计每次查询走访的区间索引节点数。构造中，覆盖新事故的
// 保单数恒定（仅 3 张同标的有效保单，其余保单属于另一标的），而事故总数
// N 从 200 增长到 3200。登记一次新事故所走访的索引节点数必须保持常数级
// （按 O(log P + k log P) 的理论上界给出一个宽松常数），与 N 无关。
func TestRegistrationCostIndependentOfAccidentCount(t *testing.T) {
	measure := func(nAccidents int) (inspected int, recomputeDelta int) {
		s := claim.NewSystem()
		// 固定数量的覆盖保单（subject="t"）。
		for i := 0; i < 3; i++ {
			if err := s.AddPolicy(claim.Policy{
				ID: fmt.Sprintf("cover%d", i), Subject: "t", Limit: 1_000_000_000, Deductible: 0,
				StartDay: 0, EndDay: 1_000_000, Insurer: "I",
			}); err != nil {
				t.Fatal(err)
			}
		}
		// 用另一标的填充保单，证明索引走访与“其他标的保单数”同样无关
		// （每标的独立索引）。
		for i := 0; i < 500; i++ {
			if err := s.AddPolicy(claim.Policy{
				ID: fmt.Sprintf("other%d", i), Subject: "other", Limit: 1, Deductible: 0,
				StartDay: 0, EndDay: 1, Insurer: "I",
			}); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < nAccidents; i++ {
			if _, err := s.RegisterAccident(claim.Accident{
				ID: fmt.Sprintf("a%d", i), Subject: "t", Day: 5, Loss: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
		before := s.Stats()
		if _, err := s.RegisterAccident(claim.Accident{
			ID: "probe", Subject: "t", Day: 5, Loss: 1,
		}); err != nil {
			t.Fatal(err)
		}
		after := s.Stats()
		return after.IndexNodesInspected - before.IndexNodesInspected,
			after.RecomputeAccidents - before.RecomputeAccidents
	}

	baseline, baseRecompute := measure(200)
	for _, n := range []int{400, 800, 1600, 3200} {
		inspected, recomputeDelta := measure(n)
		if inspected != baseline {
			t.Fatalf("index work changed with accident count: n=%d inspected=%d baseline(200)=%d",
				n, inspected, baseline)
		}
		if recomputeDelta != baseRecompute || recomputeDelta != 1 {
			t.Fatalf("registration must recompute exactly 1 accident regardless of history, got %d", recomputeDelta)
		}
		t.Logf("n=%d new registration: treap nodes inspected=%d, accidents recomputed=%d", n, inspected, recomputeDelta)
	}
	t.Logf("VERDICT: registration work = %d inspected node visits, invariant as N grows 200->3200", baseline)
}

// 更正开销只与受影响事故数相关，不随未受影响事故数增长。
//
// 构造：先登记 K 个“前缀”事故（更正点之前，不应被重算），再登记 M 个受
// 影响事故。统计一次更正触发的重算事故数：必须恰为 M+1（更正事故本身 +
// 其后全部事故），且保单前缀恢复是 O(1) 台账查找，统计的前缀查找次数只与
// 受影响事故涉及的保单数有关。
func TestCorrectionCostOnlyAffected(t *testing.T) {
	measure := func(prefix, suffix int) (recomputed, ledgerLookups int) {
		s := claim.NewSystem()
		for i := 0; i < 4; i++ {
			if err := s.AddPolicy(claim.Policy{
				ID: fmt.Sprintf("p%d", i), Subject: "t", Limit: 1_000_000_000, Deductible: 0,
				StartDay: 0, EndDay: 1_000_000, Insurer: "I",
			}); err != nil {
				t.Fatal(err)
			}
		}
		total := prefix + suffix
		for i := 0; i < total; i++ {
			if _, err := s.RegisterAccident(claim.Accident{
				ID: fmt.Sprintf("a%d", i), Subject: "t", Day: 5, Loss: int64(1 + i%5),
			}); err != nil {
				t.Fatal(err)
			}
		}
		correctID := fmt.Sprintf("a%d", prefix)
		before := s.Stats()
		if _, err := s.CorrectAccident(claim.CorrectAccidentInput{AccidentID: correctID, NewLoss: 7}); err != nil {
			t.Fatal(err)
		}
		after := s.Stats()
		return after.RecomputeAccidents - before.RecomputeAccidents,
			after.PrefixLedgerEntries - before.PrefixLedgerEntries
	}

	// 受影响规模固定为 suffix=50，未受影响前缀 200 -> 3200。
	wantRecompute := 50
	baselineLookups := 0
	for run, prefix := range []int{200, 400, 800, 1600, 3200} {
		recomputed, lookups := measure(prefix, 50)
		if recomputed != wantRecompute {
			t.Fatalf("prefix=%d: recomputed=%d want exactly %d (corrected + suffix)",
				prefix, recomputed, wantRecompute)
		}
		if run == 0 {
			baselineLookups = lookups
		} else if lookups != baselineLookups {
			t.Fatalf("prefix=%d: prefix ledger lookups %d != baseline %d (must not scale with unaffected accidents)",
				prefix, lookups, baselineLookups)
		}
		t.Logf("prefix=%d suffix=50: accidents recomputed=%d, prefix-restore lookups=%d",
			prefix, recomputed, lookups)
	}
	t.Logf("VERDICT: correction recomputed exactly %d accidents and %d O(1) prefix lookups; unaffected prefix grew 200->3200 with no effect",
		wantRecompute, baselineLookups)
}
