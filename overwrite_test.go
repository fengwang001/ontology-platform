package ontology

import (
	"math"
	"reflect"
	"testing"
)

// 后到的分数覆盖先到的分数，并立即体现在排名中。
func TestOverwriteTakesEffectImmediately(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 1)
	sel.Push("b", 2)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("before overwrite: %v", got)
	}
	sel.Push("a", 100)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("after overwrite: %v", got)
	}
	if sel.Len() != 2 {
		t.Fatalf("len must stay 2, got %d", sel.Len())
	}
}

// 覆盖后分数变差到掉出 Top-K：该 ID 必须立即从保留集合消失。
func TestOverwriteWorseDropsOutOfTopK(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 100)
	sel.Push("b", 50)
	sel.Push("a", 1) // a 降为 1，比 b 的 50 差，应掉出

	got := sel.Snapshot()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("a should have dropped out: %+v", got)
	}
}

// Asc 方向下覆盖为更大的分数也应掉出。
func TestOverwriteWorseDropsOutOfTopKAsc(t *testing.T) {
	sel, _ := New(2, Asc)
	sel.Push("a", 1)
	sel.Push("b", 50)
	sel.Push("a", 100) // a 变大，掉出最小的两个

	got := sel.Snapshot()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("a should have dropped out (asc): %+v", got)
	}
}

// Snapshot 中同一 ID 绝不出现两次；反复覆盖也不产生副本。
func TestNoDuplicateIDAfterRepeatedOverwrites(t *testing.T) {
	sel, _ := New(3, Desc)
	scores := []float64{5, 9, 2, 7, 3, 100, -4, 6}
	for i, s := range scores {
		// 一半更新打到固定 ID 上。
		id := "fixed"
		if i%2 == 1 {
			id = "other"
		}
		sel.Push(id, s)
	}
	seen := map[string]int{}
	for _, e := range sel.Snapshot() {
		seen[e.ID]++
	}
	if len(seen) != len(sel.Snapshot()) {
		t.Fatalf("duplicate IDs in snapshot: %v", seen)
	}
}

// 掉出 Top-K 的 ID 以更好分数重新 Push，应当能重新入选。
func TestDroppedIDCanReturn(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 100)
	sel.Push("b", 50)
	sel.Push("a", 1) // a 掉出
	sel.Push("c", 60)
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("before return: %v", got)
	}
	sel.Push("a", 70) // a 回来并超过 c
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("after return: %v", got)
	}
}

// overwriteCase 用同一组字段覆盖同 ID 覆盖的全部形态：
// 非满/满容器 × Desc/Asc × 更差/更优/相同/并列分数。
// 所有断言钉住当前真实行为（见 FINDINGS.md），不代表期望语义。
type overwriteCase struct {
	name        string
	dir         Direction
	k           int
	seed        []Element // 覆盖前依次 Push
	id          string
	score       float64
	wantIDs     []string  // 覆盖后 Snapshot 的 ID 顺序
	wantScore   []float64 // 与 wantIDs 一一对应的最终分数
	wantLen     int
	wantSkipped int
}

func overwriteCases() []overwriteCase {
	return []overwriteCase{
		// ---- 非满容器（len < K）：覆盖为更差分数 ----
		{
			name:      "underfull/desc worse keeps element",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"a", 10}, {"b", 5}},
			id:        "a",
			score:     1, // Desc：1 比 5 差，且容器未满
			wantIDs:   []string{"b"},
			wantScore: []float64{5},
			wantLen:   1, // 真实行为：a 被 replace 误删，仅剩 b
		},
		{
			name:      "underfull/asc worse keeps element",
			dir:       Asc,
			k:         3,
			seed:      []Element{{"a", 1}, {"b", 5}},
			id:        "a",
			score:     10, // Asc：10 比 5 差，且容器未满
			wantIDs:   []string{"b"},
			wantScore: []float64{5},
			wantLen:   1, // 真实行为：a 被误删
		},
		{
			name:      "underfull/desc worse with two survivors still dropped",
			dir:       Desc,
			k:         4,
			seed:      []Element{{"a", 100}, {"b", 80}, {"c", 60}},
			id:        "a",
			score:     1,
			wantIDs:   []string{"b", "c"},
			wantScore: []float64{80, 60},
			wantLen:   2,
		},
		// ---- 非满容器：覆盖为更优分数（对照，应正常更新） ----
		{
			name:      "underfull/desc better updates in place",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"a", 1}, {"b", 5}},
			id:        "a",
			score:     9,
			wantIDs:   []string{"a", "b"},
			wantScore: []float64{9, 5},
			wantLen:   2,
		},
		{
			name:      "underfull/asc better updates in place",
			dir:       Asc,
			k:         3,
			seed:      []Element{{"a", 9}, {"b", 5}},
			id:        "a",
			score:     1,
			wantIDs:   []string{"a", "b"},
			wantScore: []float64{1, 5},
			wantLen:   2,
		},
		// ---- 非满容器：覆盖为相同分数 ----
		{
			name:      "underfull/desc same score keeps element",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"a", 5}, {"b", 5}},
			id:        "a",
			score:     5,
			wantIDs:   []string{"a", "b"}, // a < b，并列保留
			wantScore: []float64{5, 5},
			wantLen:   2,
		},
		{
			name:      "underfull/desc tie with larger incoming id dropped",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"z", 5}, {"a", 5}},
			id:        "z",
			score:     5,
			wantIDs:   []string{"a"},
			wantScore: []float64{5},
			wantLen:   1, // 真实行为：阈值元素 a 与 incoming 并列且 z>a，z 被误删
		},
		{
			name:      "underfull/desc tie with smaller incoming id kept",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"m", 5}, {"z", 5}},
			id:        "m",
			score:     5,
			wantIDs:   []string{"m", "z"},
			wantScore: []float64{5, 5},
			wantLen:   2,
		},
		// ---- 非满容器：覆盖后分数恰好等于阈值（并列），按 ID 字典序 ----
		{
			name:      "underfull/desc new score ties threshold larger id dropped",
			dir:       Desc,
			k:         4,
			seed:      []Element{{"zeta", 9}, {"b", 7}, {"c", 7}},
			id:        "zeta",
			score:     7, // 新分数与阈值 7 并列；阈值并列元素取 ID 最大者 c
			wantIDs:   []string{"b", "c"},
			wantScore: []float64{7, 7},
			wantLen:   2, // 真实行为：zeta 被误删，尽管第 3、4 个槽位仍空
		},
		{
			name:      "underfull/desc new score ties threshold smaller id kept",
			dir:       Desc,
			k:         4,
			seed:      []Element{{"aa", 9}, {"b", 7}, {"c", 7}},
			id:        "aa",
			score:     7, // aa < b < c：严格排在阈值元素 c 之前
			wantIDs:   []string{"aa", "b", "c"},
			wantScore: []float64{7, 7, 7},
			wantLen:   3,
		},
		{
			name:      "underfull/asc new score ties threshold larger id dropped",
			dir:       Asc,
			k:         4,
			seed:      []Element{{"zeta", 1}, {"b", 3}, {"c", 3}},
			id:        "zeta",
			score:     3, // Asc：分数变大（更差），与阈值 3 并列
			wantIDs:   []string{"b", "c"},
			wantScore: []float64{3, 3},
			wantLen:   2,
		},
		// ---- 单元素容器：len==1 走 replace 的特判分支，永远保留 ----
		{
			name:      "underfull/single element desc any worse score retained",
			dir:       Desc,
			k:         1,
			seed:      []Element{{"a", 100}},
			id:        "a",
			score:     -999,
			wantIDs:   []string{"a"},
			wantScore: []float64{-999},
			wantLen:   1,
		},
		// ---- +0.0 / -0.0 判等覆盖 ----
		{
			name:      "underfull/desc -0 overwrites +0 as tie",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"a", 0}, {"b", 0}},
			id:        "a",
			score:     math.Copysign(0, -1),
			wantIDs:   []string{"a", "b"}, // ±0 判等，按 ID 升序，a 保留
			wantScore: []float64{math.Copysign(0, -1), 0},
			wantLen:   2,
		},
		{
			name:      "underfull/desc -0 overwrites +0 larger id dropped",
			dir:       Desc,
			k:         3,
			seed:      []Element{{"z", 0}, {"a", 0}},
			id:        "z",
			score:     math.Copysign(0, -1),
			wantIDs:   []string{"a"}, // z>a，并列时 incoming 不占优
			wantScore: []float64{0},
			wantLen:   1,
		},
		// ---- NaN 覆盖既有 ID：整体拒绝，旧分数保留 ----
		{
			name:        "nan overwrite underfull keeps old score",
			dir:         Desc,
			k:           3,
			seed:        []Element{{"a", 9}, {"b", 4}},
			id:          "a",
			score:       math.NaN(),
			wantIDs:     []string{"a", "b"},
			wantScore:   []float64{9, 4},
			wantLen:     2,
			wantSkipped: 1,
		},
		{
			name:        "nan overwrite single element keeps old score",
			dir:         Asc,
			k:           1,
			seed:        []Element{{"only", 3}},
			id:          "only",
			score:       math.NaN(),
			wantIDs:     []string{"only"},
			wantScore:   []float64{3},
			wantLen:     1,
			wantSkipped: 1,
		},
		// ---- 满容器（len == K）对照：与现有测试一致 ----
		{
			name:      "full/desc worse overwrite dropped",
			dir:       Desc,
			k:         2,
			seed:      []Element{{"a", 100}, {"b", 50}},
			id:        "a",
			score:     1,
			wantIDs:   []string{"b"},
			wantScore: []float64{50},
			wantLen:   1,
		},
		{
			name:      "full/asc worse overwrite dropped",
			dir:       Asc,
			k:         2,
			seed:      []Element{{"a", 1}, {"b", 50}},
			id:        "a",
			score:     100,
			wantIDs:   []string{"b"},
			wantScore: []float64{50},
			wantLen:   1,
		},
		{
			name:      "full/desc overwrite ties cutoff smaller id kept",
			dir:       Desc,
			k:         2,
			seed:      []Element{{"z", 10}, {"b", 5}},
			id:        "z",
			score:     5, // 与阈值 b 的 5 并列；z>b，不严格更优
			wantIDs:   []string{"b"},
			wantScore: []float64{5},
			wantLen:   1,
		},
		{
			name:      "full/desc overwrite ties cutoff incoming smaller id kept",
			dir:       Desc,
			k:         2,
			seed:      []Element{{"m", 10}, {"z", 5}},
			id:        "m",
			score:     5, // m<z：并列时 incoming 更优，替换为 m
			wantIDs:   []string{"m", "z"},
			wantScore: []float64{5, 5},
			wantLen:   2, // 注意：与 Push 满容器路径不同，replace 不踢出阈值元素
		},
		{
			name:      "full/desc better overwrite reorders",
			dir:       Desc,
			k:         2,
			seed:      []Element{{"a", 1}, {"b", 5}},
			id:        "a",
			score:     9,
			wantIDs:   []string{"a", "b"},
			wantScore: []float64{9, 5},
			wantLen:   2,
		},
	}
}

// TestOverwriteCharacterization 以表驱动钉住同 ID 覆盖在满/非满容器下的真实行为。
// 遍历所有覆盖形态，非满容器下被误删的用例是本测试要固化的缺陷（见 FINDINGS.md）。
func TestOverwriteCharacterization(t *testing.T) {
	for _, tc := range overwriteCases() {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := New(tc.k, tc.dir)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			pushAll(sel, tc.seed)
			sel.Push(tc.id, tc.score)

			snap := sel.Snapshot()
			if got := ids(snap); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Fatalf("snapshot ids: got %v want %v", got, tc.wantIDs)
			}
			if len(snap) != len(tc.wantScore) {
				t.Fatalf("snapshot len: got %d want %d", len(snap), len(tc.wantScore))
			}
			for i, want := range tc.wantScore {
				got := snap[i].Score
				// NaN 永不进入 Snapshot；±0 需要区分符号位，用直接比较。
				if got != want && !(got == 0 && want == 0 && math.Signbit(got) == math.Signbit(want)) {
					t.Fatalf("snapshot[%d] score: got %v want %v", i, got, want)
				}
			}
			if sel.Len() != tc.wantLen {
				t.Fatalf("Len: got %d want %d", sel.Len(), tc.wantLen)
			}
			if sel.Skipped() != tc.wantSkipped {
				t.Fatalf("Skipped: got %d want %d", sel.Skipped(), tc.wantSkipped)
			}
		})
	}
}
