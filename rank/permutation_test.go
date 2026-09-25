package rank

import (
	"reflect"
	"testing"
)

// permutations 返回 n 个下标的全部排列（n! 个）。
func permutations(n int) [][]int {
	var out [][]int
	var walk func(prefix []int, used []bool)
	walk = func(prefix []int, used []bool) {
		if len(prefix) == n {
			cp := make([]int, n)
			copy(cp, prefix)
			out = append(out, cp)
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			walk(append(prefix, i), used)
			used[i] = false
		}
	}
	walk(nil, make([]bool, n))
	return out
}

func permuteRows(rows []Row, p []int) []Row {
	out := make([]Row, len(rows))
	for i, j := range p {
		out[i] = rows[j]
	}
	return out
}

func TestPermutationStable(t *testing.T) {
	base := makeRows("p", []float64{10, 20, 20, 30})
	perms := permutations(4)
	if len(perms) < 20 {
		t.Fatalf("排列数 %d 小于 20", len(perms))
	}
	want := RankRows(base, Asc).Rankings
	for n, p := range perms {
		got := RankRows(permuteRows(base, p), Asc).Rankings
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("排列 %d (%v) 结果不一致:\n got=%v\nwant=%v", n, p, got, want)
		}
	}
}

func TestTieOrderByIDNotIndex(t *testing.T) {
	// 故意让输入下标顺序与 ID 字典序相反，且全部并列。
	rows := []Row{
		{Partition: ptr("p"), Value: 7, ID: "d"},
		{Partition: ptr("p"), Value: 7, ID: "c"},
		{Partition: ptr("p"), Value: 7, ID: "b"},
		{Partition: ptr("p"), Value: 7, ID: "a"},
	}
	res := RankRows(rows, Asc)
	for i, wantID := range []string{"a", "b", "c", "d"} {
		if res.Rankings[i].ID != wantID || res.Rankings[i].RowNumber != i+1 {
			t.Fatalf("第 %d 行 = %+v, 期望 ID=%s ROW_NUMBER=%d",
				i, res.Rankings[i], wantID, i+1)
		}
	}
	// 再打乱 20 次确认每次都一致。
	want := res.Rankings
	perms := permutations(4)
	for n := 0; n < 20; n++ {
		shuffled := permuteRows(rows, perms[n])
		if got := RankRows(shuffled, Asc).Rankings; !reflect.DeepEqual(got, want) {
			t.Fatalf("打乱 %d: %v != %v", n, got, want)
		}
	}
}
