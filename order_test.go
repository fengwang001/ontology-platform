package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

func shuffled(rows []map[string]any, seed int64) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func buildTables() (left, right []map[string]any) {
	for k := 0; k < 30; k++ {
		for i := 0; i < k%4+1; i++ {
			left = append(left, map[string]any{"id": k, "lv": i, "tag": "L"})
		}
		for j := 0; j < k%3+1; j++ {
			right = append(right, map[string]any{"id": k, "rv": j})
		}
	}
	left = append(left,
		map[string]any{"id": nil, "lv": -1},  // 空键
		map[string]any{"lv": -2},             // 缺键
		map[string]any{"id": 9999, "lv": -3}, // 右表无对应
	)
	return left, right
}

// 左右表各自任意打乱后重新连接，结果序列逐元素完全一致。
func TestShuffleInvariance(t *testing.T) {
	left, right := buildTables()
	for _, mode := range []Mode{Inner, Left} {
		base, _, err := Join(left, right, []string{"id"}, mode)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		for seed := int64(1); seed <= 20; seed++ {
			got, _, err := Join(shuffled(left, seed), shuffled(right, seed*7),
				[]string{"id"}, mode)
			if err != nil {
				t.Fatalf("Join: %v", err)
			}
			if !reflect.DeepEqual(base, got) {
				t.Fatalf("mode=%v seed=%d: shuffled result differs", mode, seed)
			}
		}
	}
}

// 结果按连接键逐列升序，同键内按行标识（内容编码位次）先左后右。
func TestDeterministicOrder(t *testing.T) {
	left := []map[string]any{
		{"a": 2, "b": "y", "lv": 1},
		{"a": 1, "b": "z", "lv": 2},
		{"a": 1, "b": "y", "lv": 3},
		{"a": 1, "b": "y", "lv": 0}, // 同键，内容编码最小，应排最前
	}
	right := []map[string]any{
		{"a": 1, "b": "y", "rv": 9},
		{"a": 2, "b": "y", "rv": 8},
		{"a": 1, "b": "y", "rv": 7}, // 同键，内容编码较小，应排在前
	}
	out, _, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("rows = %d, want 5", len(out))
	}
	type pair struct{ lv, rv int }
	got := make([]pair, len(out))
	for i, row := range out {
		got[i] = pair{row["lv"].(int), row["right.rv"].(int)}
	}
	// 键 (1,y) 组在 (2,y) 组前；组内左行 lv=0 在 lv=3 前，右行 rv=7 在 rv=9 前。
	want := []pair{{0, 7}, {0, 9}, {3, 7}, {3, 9}}
	// (2,y) 只有一对，排在最后。
	want = append(want, pair{1, 8})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// 键排序跨类型：数值 < 字符串 < 布尔（同侧混合类型时）。
func TestKeyOrderingAcrossTypes(t *testing.T) {
	left := []map[string]any{
		{"id": true}, {"id": "b"}, {"id": 2}, {"id": "a"}, {"id": -1},
	}
	out, _, err := Join(left, nil, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	want := []any{-1, 2, "a", "b", true}
	if len(out) != len(want) {
		t.Fatalf("rows = %d, want %d", len(out), len(want))
	}
	for i, row := range out {
		if row["id"] != want[i] {
			t.Fatalf("row %d id = %v, want %v", i, row["id"], want[i])
		}
	}
}

// 完全相同的重复行：展开数量正确且结果确定。
func TestIdenticalRowsExpand(t *testing.T) {
	left := []map[string]any{{"id": 1, "v": "x"}, {"id": 1, "v": "x"}}
	right := []map[string]any{{"id": 1, "w": "y"}, {"id": 1, "w": "y"}}
	base, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(base) != 4 || stats.MatchedPairs != 4 {
		t.Fatalf("rows=%d pairs=%d, want 4/4", len(base), stats.MatchedPairs)
	}
	got, _, _ := Join(shuffled(left, 5), shuffled(right, 6), []string{"id"}, Inner)
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("identical-row join not shuffle invariant")
	}
}
