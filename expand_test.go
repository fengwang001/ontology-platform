package ontology

import (
	"fmt"
	"testing"
)

// 3x2 重复键恰好展开 6 行，每对一次。
func TestExpansionThreeByTwo(t *testing.T) {
	left := []map[string]any{
		{"id": 1, "v": "a"}, {"id": 1, "v": "b"}, {"id": 1, "v": "c"},
	}
	right := []map[string]any{
		{"id": 1, "w": "x"}, {"id": 1, "w": "y"},
	}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 6 || stats.OutputRows != 6 || stats.MatchedPairs != 6 {
		t.Fatalf("rows=%d stats=%+v, want 6", len(out), stats)
	}
	if stats.MaxKeyExpansion != 6 {
		t.Fatalf("MaxKeyExpansion = %d, want 6", stats.MaxKeyExpansion)
	}
	// 每个 (左,右) 组合恰好出现一次。
	seen := map[string]int{}
	for _, row := range out {
		seen[row["v"].(string)+row["right.w"].(string)]++
	}
	if len(seen) != 6 {
		t.Fatalf("distinct pairs = %d, want 6: %v", len(seen), seen)
	}
	for combo, n := range seen {
		if n != 1 {
			t.Fatalf("pair %s appears %d times", combo, n)
		}
	}
}

// 多键混合：展开总数等于逐键 m*n 之和，MaxKeyExpansion 是单键最大。
func TestExpansionTotalsMatchPerKeySum(t *testing.T) {
	sizes := []struct{ m, n int }{{3, 2}, {1, 4}, {5, 5}, {2, 0}, {0, 3}}
	var left, right []map[string]any
	wantSum, wantMax := 0, 0
	for k, sz := range sizes {
		for i := 0; i < sz.m; i++ {
			left = append(left, map[string]any{"id": k, "lv": i})
		}
		for j := 0; j < sz.n; j++ {
			right = append(right, map[string]any{"id": k, "rv": j})
		}
		if sz.m*sz.n > 0 {
			wantSum += sz.m * sz.n
			if sz.m*sz.n > wantMax {
				wantMax = sz.m * sz.n
			}
		}
	}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	perKeySum := 0
	for _, mn := range stats.KeyExpansions() {
		perKeySum += mn
	}
	if perKeySum != wantSum || stats.MatchedPairs != wantSum || len(out) != wantSum {
		t.Fatalf("perKey=%d pairs=%d rows=%d, want %d",
			perKeySum, stats.MatchedPairs, len(out), wantSum)
	}
	if stats.MaxKeyExpansion != wantMax {
		t.Fatalf("MaxKeyExpansion = %d, want %d", stats.MaxKeyExpansion, wantMax)
	}
	// 只在左/右出现的键不产生展开明细。
	if len(stats.KeyExpansions()) != 3 {
		t.Fatalf("KeyExpansions has %d keys, want 3", len(stats.KeyExpansions()))
	}
	if stats.LeftUnmatchedRows != 2 || stats.RightUnmatchedRows != 3 {
		t.Fatalf("unmatched = %d/%d, want 2/3",
			stats.LeftUnmatchedRows, stats.RightUnmatchedRows)
	}
}

// 上万行输入：总数与逐键求和一致，且每对只出现一次。
func TestExpansionAtScale(t *testing.T) {
	const keys = 200
	var left, right []map[string]any
	wantSum := 0
	for k := 0; k < keys; k++ {
		m, n := k%17+1, k%13+1
		for i := 0; i < m; i++ {
			left = append(left, map[string]any{"id": k, "lv": i})
		}
		for j := 0; j < n; j++ {
			right = append(right, map[string]any{"id": k, "rv": j})
		}
		wantSum += m * n
	}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	perKeySum := 0
	for _, mn := range stats.KeyExpansions() {
		perKeySum += mn
	}
	if len(out) != wantSum || stats.MatchedPairs != wantSum || perKeySum != wantSum {
		t.Fatalf("rows=%d pairs=%d perKey=%d, want %d",
			len(out), stats.MatchedPairs, perKeySum, wantSum)
	}
	seen := map[string]int{}
	for _, row := range out {
		seen[fmt.Sprintf("%v/%v/%v", row["id"], row["lv"], row["right.rv"])]++
	}
	if len(seen) != wantSum {
		t.Fatalf("distinct pairs = %d, want %d", len(seen), wantSum)
	}
	for combo, n := range seen {
		if n != 1 {
			t.Fatalf("pair %s x%d", combo, n)
		}
	}
}

// KeyExpansions 返回副本，修改不影响再次读取。
func TestKeyExpansionsIsCopy(t *testing.T) {
	left := []map[string]any{{"id": 1}}
	right := []map[string]any{{"id": 1}}
	_, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	first := stats.KeyExpansions()
	for k := range first {
		first[k] = -99
	}
	for _, v := range stats.KeyExpansions() {
		if v != 1 {
			t.Fatalf("KeyExpansions mutated: %v", stats.KeyExpansions())
		}
	}
}
