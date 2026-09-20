package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Duplicate keys expand to exactly m*n pairs per key, once each.
func TestDuplicateKeyExpansion(t *testing.T) {
	var left, right []map[string]any
	for i := 0; i < 3; i++ {
		left = append(left, map[string]any{"k": "dup", "lv": i})
	}
	for i := 0; i < 2; i++ {
		right = append(right, map[string]any{"k": "dup", "rv": i})
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 6 {
		t.Fatalf("RowCount = %d, want 3*2=6", res.RowCount())
	}
	if res.MaxFanout() != 6 {
		t.Fatalf("MaxFanout = %d, want 6", res.MaxFanout())
	}
	seen := make(map[[2]int]int)
	for _, row := range res.Rows {
		pair := [2]int{row["lv"].(int), row["rv"].(int)}
		seen[pair]++
	}
	if len(seen) != 6 {
		t.Fatalf("distinct pairs = %d, want 6", len(seen))
	}
	for pair, n := range seen {
		if n != 1 {
			t.Fatalf("pair %v appears %d times, want exactly 1", pair, n)
		}
	}
}

// On thousands of rows, total output equals the per-key sum of m*n and
// MaxFanout equals the largest single-key m*n.
func TestFanoutSumMatchesTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const keys = 50
	leftCounts := make([]int, keys)
	rightCounts := make([]int, keys)
	var left, right []map[string]any
	for i := 0; i < 3000; i++ {
		k := rng.Intn(keys)
		leftCounts[k]++
		left = append(left, map[string]any{"k": int64(k), "lv": i})
	}
	for i := 0; i < 2000; i++ {
		k := rng.Intn(keys)
		rightCounts[k]++
		right = append(right, map[string]any{"k": int64(k), "rv": i})
	}
	wantTotal, wantMax := 0, 0
	for k := 0; k < keys; k++ {
		mn := leftCounts[k] * rightCounts[k]
		wantTotal += mn
		if mn > wantMax {
			wantMax = mn
		}
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != wantTotal {
		t.Fatalf("RowCount = %d, want sum of per-key m*n = %d", res.RowCount(), wantTotal)
	}
	if res.MaxFanout() != wantMax {
		t.Fatalf("MaxFanout = %d, want %d", res.MaxFanout(), wantMax)
	}
}

// Shuffling either input must not change the output sequence at all.
func TestShuffleDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var left, right []map[string]any
	for i := 0; i < 400; i++ {
		left = append(left, map[string]any{
			"k": fmt.Sprintf("k%02d", rng.Intn(20)), "lv": i, "tag": "L",
		})
	}
	for i := 0; i < 300; i++ {
		right = append(right, map[string]any{
			"k": fmt.Sprintf("k%02d", rng.Intn(20)), "rv": i,
		})
	}
	base, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	for trial := 0; trial < 5; trial++ {
		sl := shuffled(left, rng)
		sr := shuffled(right, rng)
		got, err := Join(sl, sr, []string{"k"}, Left)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		if got.RowCount() != base.RowCount() {
			t.Fatalf("trial %d: RowCount = %d, want %d", trial, got.RowCount(), base.RowCount())
		}
		for i := range base.Rows {
			if !reflect.DeepEqual(base.Rows[i], got.Rows[i]) {
				t.Fatalf("trial %d: row %d differs:\nbase=%v\ngot =%v",
					trial, i, base.Rows[i], got.Rows[i])
			}
		}
	}
}

func shuffled(rows []map[string]any, rng *rand.Rand) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// Output is sorted by key columns, then by content-derived row identity.
func TestOutputOrdering(t *testing.T) {
	left := []map[string]any{
		{"k": "b", "lv": 1},
		{"k": "a", "lv": 2},
		{"k": "b", "lv": 0},
	}
	right := []map[string]any{
		{"k": "b", "rv": 9},
		{"k": "a", "rv": 8},
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	wantKeys := []string{"a", "b", "b"}
	if len(res.Rows) != len(wantKeys) {
		t.Fatalf("RowCount = %d, want %d", len(res.Rows), len(wantKeys))
	}
	for i, want := range wantKeys {
		if res.Rows[i]["k"] != want {
			t.Fatalf("row %d key = %v, want %q", i, res.Rows[i]["k"], want)
		}
	}
	// Within key "b": left rows ordered by content (lv=0 before lv=1).
	if res.Rows[1]["lv"] != 0 || res.Rows[2]["lv"] != 1 {
		t.Fatalf("within-key order wrong: %v %v",
			res.Rows[1]["lv"], res.Rows[2]["lv"])
	}
}
