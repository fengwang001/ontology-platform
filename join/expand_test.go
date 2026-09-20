package join

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestDuplicateKeyExpansion(t *testing.T) {
	left := []Row{
		{"id": 1, "v": "l1"}, {"id": 1, "v": "l2"}, {"id": 1, "v": "l3"},
	}
	right := []Row{
		{"id": 1, "w": "r1"}, {"id": 1, "w": "r2"},
	}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 6 {
		t.Fatalf("rows = %d, want 3*2=6", len(res.Rows))
	}
	seen := make(map[[2]string]int)
	for _, r := range res.Rows {
		seen[[2]string{r["v"].(string), r["right.w"].(string)}]++
	}
	if len(seen) != 6 {
		t.Fatalf("distinct pairs = %d, want 6", len(seen))
	}
	for pair, n := range seen {
		if n != 1 {
			t.Fatalf("pair %v appeared %d times, want exactly 1", pair, n)
		}
	}
	if res.Stats.MaxKeyExpansion != 6 || res.Stats.MatchedPairs != 6 {
		t.Fatalf("stats = %+v, want expansion 6 and pairs 6", res.Stats)
	}
}

func TestExpansionSumMatchesPerKeyProduct(t *testing.T) {
	// 多个键，每个键左 m 行、右 n 行；总产出行数须等于逐键 m*n 之和。
	rng := rand.New(rand.NewSource(42))
	var left, right []Row
	wantSum, wantMax := 0, 0
	for k := 0; k < 50; k++ {
		m, n := rng.Intn(8), rng.Intn(8)
		for i := 0; i < m; i++ {
			left = append(left, Row{"id": k, "v": fmt.Sprintf("l-%d-%d", k, i)})
		}
		for j := 0; j < n; j++ {
			right = append(right, Row{"id": k, "w": fmt.Sprintf("r-%d-%d", k, j)})
		}
		wantSum += m * n
		if m*n > wantMax {
			wantMax = m * n
		}
	}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.OutputRows != wantSum || res.Stats.MatchedPairs != wantSum {
		t.Fatalf("OutputRows=%d MatchedPairs=%d, want sum of m*n = %d",
			res.Stats.OutputRows, res.Stats.MatchedPairs, wantSum)
	}
	if res.Stats.MaxKeyExpansion != wantMax {
		t.Fatalf("MaxKeyExpansion = %d, want %d", res.Stats.MaxKeyExpansion, wantMax)
	}
	if len(res.Rows) != wantSum {
		t.Fatalf("len(Rows) = %d, want %d", len(res.Rows), wantSum)
	}
}

func TestShuffleDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var left, right []Row
	for i := 0; i < 300; i++ {
		left = append(left, Row{"id": i % 40, "v": fmt.Sprintf("l%d", i)})
		right = append(right, Row{"id": (i * 3) % 40, "w": fmt.Sprintf("r%d", i)})
	}
	base, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < 5; trial++ {
		sl := shuffle(rng, left)
		sr := shuffle(rng, right)
		got, err := Join(sl, sr, []string{"id"}, Left)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(base.Rows, got.Rows) {
			t.Fatalf("trial %d: shuffled join differs from base", trial)
		}
		if base.Stats != got.Stats {
			t.Fatalf("trial %d: stats differ: %+v vs %+v", trial, base.Stats, got.Stats)
		}
	}
}

func shuffle(rng *rand.Rand, rows []Row) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func TestOutputSortedByKeyThenRowID(t *testing.T) {
	left := []Row{{"id": 2, "v": "b"}, {"id": 1, "v": "a"}, {"id": 1, "v": "c"}}
	right := []Row{{"id": 1}, {"id": 2}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	var gotKeys []int
	for _, r := range res.Rows {
		gotKeys = append(gotKeys, r["id"].(int))
	}
	if !reflect.DeepEqual(gotKeys, []int{1, 1, 2}) {
		t.Fatalf("key order = %v, want [1 1 2]", gotKeys)
	}
	// 同键内按行标识（内容派生）升序："a" 的行标识小于 "c"。
	if res.Rows[0]["v"] != "a" || res.Rows[1]["v"] != "c" {
		t.Fatalf("same-key order = %v, %v; want a then c", res.Rows[0]["v"], res.Rows[1]["v"])
	}
}
