package ranking

import (
	"math/rand"
	"testing"
)

// 同一批行打乱至少 20 种排列后，输出必须逐行完全相同（含 ROW_NUMBER）。
// 并列内部次序只能由行 ID 升序决定，不得依赖输入下标或 map 迭代顺序。
func TestRankDeterministicAcrossPermutations(t *testing.T) {
	base := []Row{
		{Partition: strPtr("b"), Value: 20, ID: "delta"},
		{Partition: strPtr("a"), Value: 20, ID: "alpha"},
		{Partition: strPtr("a"), Value: 10, ID: "bravo"},
		{Partition: strPtr("a"), Value: 20, ID: "charlie"},
		{Partition: strPtr("b"), Value: 20, ID: "echo"},
		{Partition: strPtr("a"), Value: 30, ID: "foxtrot"},
		{Partition: strPtr("b"), Value: 5, ID: "golf"},
	}
	want := Rank(base, Options{})
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([]Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := Rank(shuffled, Options{})
		assertSameResult(t, trial, want, got)
	}
}

func assertSameResult(t *testing.T, trial int, want, got Result) {
	t.Helper()
	if got.Skipped != want.Skipped {
		t.Fatalf("trial %d: Skipped = %d, want %d", trial, got.Skipped, want.Skipped)
	}
	if len(got.Rows) != len(want.Rows) {
		t.Fatalf("trial %d: len(Rows) = %d, want %d", trial, len(got.Rows), len(want.Rows))
	}
	for i := range want.Rows {
		w, g := want.Rows[i], got.Rows[i]
		if g.Row.ID != w.Row.ID || g.RowNumber != w.RowNumber ||
			g.Rank != w.Rank || g.DenseRank != w.DenseRank {
			t.Errorf("trial %d row %d: got (%s,%d,%d,%d), want (%s,%d,%d,%d)",
				trial, i, g.Row.ID, g.RowNumber, g.Rank, g.DenseRank,
				w.Row.ID, w.RowNumber, w.Rank, w.DenseRank)
		}
	}
}

// 并列内部 ROW_NUMBER 必须由行 ID 升序决定，与输入顺序无关。
func TestTieBreakByRowIDAscending(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 7, ID: "zeta"},
		{Partition: strPtr("p"), Value: 7, ID: "alpha"},
		{Partition: strPtr("p"), Value: 7, ID: "mike"},
	}
	got := Rank(rows, Options{})
	wantIDs := []string{"alpha", "mike", "zeta"}
	for i, id := range wantIDs {
		if got.Rows[i].Row.ID != id || got.Rows[i].RowNumber != i+1 {
			t.Errorf("row %d: got (%s, rn=%d), want (%s, rn=%d)",
				i, got.Rows[i].Row.ID, got.Rows[i].RowNumber, id, i+1)
		}
	}
}
