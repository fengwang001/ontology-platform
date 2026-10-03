package disclose

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func supSet(t *Table) map[[2]int]bool {
	m := map[[2]int]bool{}
	for i, row := range t.Cells {
		for j, c := range row {
			if c.Suppressed {
				m[[2]int{i, j}] = true
			}
		}
	}
	return m
}

func TestSpecExample1(t *testing.T) {
	counts := [][]int{{9, 1, 8}, {7, 6, 9}, {8, 9, 2}}
	tb := Suppress([]string{"r1", "r2", "r3"}, []string{"c1", "c2", "c3"}, counts, 3)
	wantSup := map[[2]int]bool{{0, 1}: true, {0, 2}: true, {1, 0}: true, {1, 1}: true, {2, 0}: true, {2, 2}: true}
	if !reflect.DeepEqual(supSet(&tb), wantSup) {
		t.Fatalf("抑制格 = %v, want %v", supSet(&tb), wantSup)
	}
	if tb.Primary != 2 || tb.Complementary != 4 {
		t.Fatalf("Primary=%d Complementary=%d, want 2/4", tb.Primary, tb.Complementary)
	}
	if !reflect.DeepEqual(tb.RowTotals, []int{18, 22, 19}) ||
		!reflect.DeepEqual(tb.ColTotals, []int{24, 16, 19}) || tb.Total != 59 {
		t.Fatalf("合计错误: %v %v %d", tb.RowTotals, tb.ColTotals, tb.Total)
	}
	if tb.Cells[0][0].Count != 9 || tb.Cells[1][2].Count != 9 || tb.Cells[2][1].Count != 9 {
		t.Fatalf("可见格计数错误")
	}
	t.Logf("输入 %v k=3; 主抑制 r1c2,r3c3; 行趟补 r1c3,r3c1; 列趟补 r2c1,r2c2; 输出 %+v", counts, tb.RowTotals)
}

func TestSpecExample2ZeroCellAndHiddenTotals(t *testing.T) {
	counts := [][]int{{1, 5}, {0, 4}}
	tb := Suppress([]string{"r1", "r2"}, []string{"c1", "c2"}, counts, 3)
	wantSup := map[[2]int]bool{{0, 0}: true, {0, 1}: true, {1, 1}: true}
	if !reflect.DeepEqual(supSet(&tb), wantSup) {
		t.Fatalf("抑制格 = %v, want %v", supSet(&tb), wantSup)
	}
	if tb.Primary != 1 || tb.Complementary != 2 {
		t.Fatalf("Primary=%d Complementary=%d, want 1/2", tb.Primary, tb.Complementary)
	}
	if tb.Cells[1][0].Count != 0 || tb.Cells[1][0].Suppressed {
		t.Fatalf("r2c1 应为可见的 0 格: %+v", tb.Cells[1][0])
	}
	if !reflect.DeepEqual(tb.RowTotals, []int{6, -1}) ||
		!reflect.DeepEqual(tb.ColTotals, []int{-1, 9}) || tb.Total != -1 {
		t.Fatalf("合计隐藏错误: %v %v %d", tb.RowTotals, tb.ColTotals, tb.Total)
	}
	t.Logf("输入 %v k=3; r2 行与 c1 列恰 1 个被抑制格且无候选, 合计与总计隐藏", counts)
}

func TestKBoundary(t *testing.T) {
	at := Suppress([]string{"r"}, []string{"c"}, [][]int{{3}}, 3)
	if at.Cells[0][0].Suppressed || at.Cells[0][0].Count != 3 || at.Total != 3 {
		t.Fatalf("c==k 不应抑制: %+v", at.Cells[0][0])
	}
	below := Suppress([]string{"r"}, []string{"c"}, [][]int{{2}}, 3)
	if !below.Cells[0][0].Suppressed || below.Cells[0][0].Count != 0 {
		t.Fatalf("c==k-1 应抑制且 Count 为 0: %+v", below.Cells[0][0])
	}
	if below.RowTotals[0] != -1 || below.ColTotals[0] != -1 || below.Total != -1 {
		t.Fatalf("唯一格被抑制且无候选, 合计应全部隐藏: %v %v %d",
			below.RowTotals, below.ColTotals, below.Total)
	}
	t.Logf("k=3: c=3 不抑制; c=2 抑制; 单行单列无互补候选, 合计全隐藏")
}

func TestTieBreakPicksSmallerIndex(t *testing.T) {
	counts := [][]int{{1, 5, 5}}
	tb := Suppress([]string{"r"}, []string{"c1", "c2", "c3"}, counts, 2)
	want := map[[2]int]bool{{0, 0}: true, {0, 1}: true}
	if !reflect.DeepEqual(supSet(&tb), want) {
		t.Fatalf("并列候选应取列下标较小者: %v", supSet(&tb))
	}
	t.Logf("输入 %v k=2; r 行候选 c2=5,c3=5 并列, 取 c2", counts)
}

func TestMultiRoundConvergence(t *testing.T) {
	counts := [][]int{{1, 9, 1}, {9, 9, 9}, {9, 1, 9}}
	tb := Suppress([]string{"r1", "r2", "r3"}, []string{"c1", "c2", "c3"}, counts, 2)
	want := map[[2]int]bool{{0, 0}: true, {0, 1}: true, {0, 2}: true,
		{1, 0}: true, {1, 2}: true, {2, 0}: true, {2, 1}: true}
	if !reflect.DeepEqual(supSet(&tb), want) {
		t.Fatalf("抑制格 = %v, want %v", supSet(&tb), want)
	}
	if tb.Primary != 3 || tb.Complementary != 4 {
		t.Fatalf("Primary=%d Complementary=%d, want 3/4", tb.Primary, tb.Complementary)
	}
	if tb.Cells[1][1].Count != 9 || tb.Cells[2][2].Count != 9 {
		t.Fatalf("可见格应为 r2c2=9, r3c3=9")
	}
	t.Logf("第 1 轮列趟使 r2 恰 1 个被抑制格, 第 2 轮行趟新增 r2c1, 多轮收敛")
}

// naiveSuppress 是按规则逐步写成的朴素模拟，用于对照。
func naiveSuppress(counts [][]int, k int) map[[2]int]bool {
	nr, nc := len(counts), len(counts[0])
	sup := map[[2]int]bool{}
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if counts[i][j] >= 1 && counts[i][j] < k {
				sup[[2]int{i, j}] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for i := 0; i < nr; i++ { // 行趟
			var supInRow []int
			for j := 0; j < nc; j++ {
				if sup[[2]int{i, j}] {
					supInRow = append(supInRow, j)
				}
			}
			if len(supInRow) != 1 {
				continue
			}
			best := -1
			for j := 0; j < nc; j++ {
				if !sup[[2]int{i, j}] && counts[i][j] > 0 &&
					(best < 0 || counts[i][j] < counts[i][best]) {
					best = j
				}
			}
			if best >= 0 {
				sup[[2]int{i, best}] = true
				changed = true
			}
		}
		for j := 0; j < nc; j++ { // 列趟
			var supInCol []int
			for i := 0; i < nr; i++ {
				if sup[[2]int{i, j}] {
					supInCol = append(supInCol, i)
				}
			}
			if len(supInCol) != 1 {
				continue
			}
			best := -1
			for i := 0; i < nr; i++ {
				if !sup[[2]int{i, j}] && counts[i][j] > 0 &&
					(best < 0 || counts[i][j] < counts[best][j]) {
					best = i
				}
			}
			if best >= 0 {
				sup[[2]int{best, j}] = true
				changed = true
			}
		}
	}
	return sup
}

func TestAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for trial := 0; trial < 300; trial++ {
		nr, nc := 1+rng.Intn(4), 1+rng.Intn(4)
		k := 1 + rng.Intn(5)
		counts := make([][]int, nr)
		rows, cols := make([]string, nr), make([]string, nc)
		for i := range rows {
			rows[i] = fmt.Sprintf("r%d", i)
			counts[i] = make([]int, nc)
			for j := range counts[i] {
				counts[i][j] = rng.Intn(8)
			}
		}
		for j := range cols {
			cols[j] = fmt.Sprintf("c%d", j)
		}
		tb := Suppress(rows, cols, counts, k)
		want := naiveSuppress(counts, k)
		if got := supSet(&tb); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d 输入 %v k=%d: 抑制格 %v, 朴素模拟 %v", trial, counts, k, got, want)
		}
		if trial < 3 {
			t.Logf("trial %d 输入 %v k=%d 抑制 %v 行合计 %v 列合计 %v 总计 %d",
				trial, counts, k, supSet(&tb), tb.RowTotals, tb.ColTotals, tb.Total)
		}
	}
}
