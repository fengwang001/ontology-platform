package underwriting

import (
	"fmt"
	"testing"
)

// buildStore 构造含 K 条相关规则与 n 条无关规则（年龄区间与职业类别
// 均与投保单不相关）的规则库。
func buildStore(t testing.TB, relevant, irrelevant int) *RuleStore {
	t.Helper()
	store := NewRuleStore()
	for i := 0; i < relevant; i++ {
		err := store.Add(Rule{
			ID:     fmt.Sprintf("rel-%d", i),
			Start:  0,
			End:    10_000,
			Cond:   Condition{MinAge: intPtr(30), MaxAge: intPtr(40), Occupations: map[int]bool{2: true}},
			Action: Action{Kind: ActionExclusion, ExclusionCode: fmt.Sprintf("X%d", i)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < irrelevant; i++ {
		err := store.Add(Rule{
			ID:     fmt.Sprintf("irr-%d", i),
			Start:  0,
			End:    10_000,
			Cond:   Condition{MinAge: intPtr(500), MaxAge: intPtr(600), Occupations: map[int]bool{4: true}},
			Action: Action{Kind: ActionDecline},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestExaminedCountIndependentOfLibrarySize 直接验证：单次裁定考察的规则数
// 不随无关规则数量增长（两档规模 1e2 与 1e5 下完全相同）。
func TestExaminedCountIndependentOfLibrarySize(t *testing.T) {
	const relevant = 8
	var counts []int
	for _, irrelevant := range []int{100, 100_000} {
		store := buildStore(t, relevant, irrelevant)
		snap := store.Snapshot()
		d, examined := decide(snap, 35, 2, map[string]bool{}, 500_000, 1_000, 50, 1_000_000, false)
		if d.Kind != DecAccept || len(d.Exclusions) != relevant {
			t.Fatalf("ir totale inatteso: %+v", d)
		}
		counts = append(counts, examined)
		t.Logf("无关规则=%d 考察规则数=%d", irrelevant, examined)
	}
	if counts[0] != counts[1] {
		t.Fatalf("考察规则数随无关规则增长: %v", counts)
	}
}

// BenchmarkDecide 对照两档规模规则库的单次裁定开销。
// 运行：go test -bench=Decide -benchmem ./underwriting
func BenchmarkDecide(b *testing.B) {
	for _, irrelevant := range []int{1_000, 100_000} {
		store := buildStore(b, 8, irrelevant)
		snap := store.Snapshot()
		disc := map[string]bool{}
		b.Run(fmt.Sprintf("irrelevant=%d", irrelevant), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decide(snap, 35, 2, disc, 500_000, 1_000, 50, 1_000_000, false)
			}
		})
	}
}
