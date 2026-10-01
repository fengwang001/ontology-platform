package bptree

import (
	"fmt"
	"strings"
	"testing"
)

// TestLastPageExactlyMin: last leaf occupancy == mL means no rebalance.
// C=5 -> mL=2, tL=5. 7 keys seal pages of 5+2; the last page is exactly 2.
func TestLastPageExactlyMin(t *testing.T) {
	l := finishLoader(t, 5, 4, 100, keysN(7))
	leaves := l.Levels()[0]
	if len(leaves) != 2 || len(leaves[0].Keys) != 5 || len(leaves[1].Keys) != 2 {
		t.Fatalf("occupancy = %v, want [5 2]", leafSizes(leaves))
	}
	t.Logf("输入 n=7 C=5 B=4 p=100 输出叶占用=%v 判定: 末页恰好等于mL=2，不再平衡", leafSizes(leaves))
}

// TestLastPageMinusOneRebalance: C=5, tL=5, 6 keys -> [5,1], last is one
// below mL=2; sum 6 > C=5 so it splits as [3,3].
func TestLastPageMinusOneRebalance(t *testing.T) {
	l := finishLoader(t, 5, 4, 100, keysN(6))
	leaves := l.Levels()[0]
	if len(leaves) != 2 || len(leaves[0].Keys) != 3 || len(leaves[1].Keys) != 3 {
		t.Fatalf("occupancy = %v, want [3 3]", leafSizes(leaves))
	}
	t.Logf("输入 n=6 C=5 p=100 输出叶占用=%v 判定: 末页1<mL=2且和6>C，均分", leafSizes(leaves))
}

// TestOddSplitFrontGetsMore: C=6,tL=6,n=7 -> [6,1], mL=3, sum=7 odd ->
// [4,3], earlier page gets ceil.
func TestOddSplitFrontGetsMore(t *testing.T) {
	l := finishLoader(t, 6, 4, 100, keysN(7))
	leaves := l.Levels()[0]
	if len(leaves) != 2 || len(leaves[0].Keys) != 4 || len(leaves[1].Keys) != 3 {
		t.Fatalf("occupancy = %v, want [4 3]", leafSizes(leaves))
	}
	got := append([]string{}, leaves[0].Keys...)
	got = append(got, leaves[1].Keys...)
	if strings.Join(got, ",") != strings.Join(keysN(7), ",") {
		t.Fatalf("keys out of order: %v", got)
	}
	t.Logf("输入 n=7 C=6 p=100 输出叶占用=%v 判定: 和为奇数7，前页得ceil=4，末页得floor=3，键序保持", leafSizes(leaves))
}

// TestMergeIntoOnePage: target == minimum and last page is one short.
// C=6 -> mL=tL=3 (p=1): 5 keys -> [3,2], sum 5 <= C -> one page [5].
func TestMergeIntoOnePage(t *testing.T) {
	l := finishLoader(t, 6, 4, 1, keysN(5))
	leaves := l.Levels()[0]
	if len(leaves) != 1 || len(leaves[0].Keys) != 5 {
		t.Fatalf("occupancy = %v, want single page of 5", leafSizes(leaves))
	}
	t.Logf("输入 n=5 C=6 p=1 (tL=mL=3) 输出叶占用=%v 判定: 末页2<3且和5<=6，合成一页", leafSizes(leaves))
}

// TestFill100And1: p=100 targets capacity; p=1 targets are lifted to minima.
func TestFill100And1(t *testing.T) {
	l100, err := New(7, 6, 100)
	if err != nil {
		t.Fatal(err)
	}
	if l100.tL != 7 || l100.tI != 6 {
		t.Fatalf("p=100 targets = %d,%d want 7,6", l100.tL, l100.tI)
	}
	l1, err := New(7, 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	if l1.tL != 3 || l1.tI != 3 {
		t.Fatalf("p=1 targets = %d,%d want 3,3", l1.tL, l1.tI)
	}
	t.Logf("判定: C=7 B=6 p=100 -> tL=7 tI=6; p=1 -> tL=max(1,3)=3 tI=max(1,3)=3")
}

// TestInternalLastGroupRebalance: B=5 -> mI=3,tI=5. C=3,p=100, 19 keys
// rebalances leaves to [3,3,3,3,3,2,2] (7 leaves); internal groups
// [5,2] -> 2<3, sum 7>B=5 -> [4,3].
func TestInternalLastGroupRebalance(t *testing.T) {
	l := finishLoader(t, 3, 5, 100, keysN(19))
	levels := l.Levels()
	if len(levels) < 2 {
		t.Fatalf("expected internal level, got height %d", len(levels))
	}
	sizes := internalSizes(levels[1])
	want := []int{4, 3}
	if fmt.Sprint(sizes) != fmt.Sprint(want) {
		t.Fatalf("internal occupancy = %v, want %v; leaves=%v",
			sizes, want, leafSizes(levels[0]))
	}
	t.Logf("输入 n=19 C=3 B=5 p=100 输出叶占用=%v 内部占用=%v 判定: 末组2<mI=3且和7>B，均分为4/3",
		leafSizes(levels[0]), sizes)
}

// TestSinglePageRoot: a single leaf is the root regardless of occupancy.
func TestSinglePageRoot(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		l := finishLoader(t, 6, 5, 100, keysN(n))
		levels := l.Levels()
		if len(levels) != 1 || len(levels[0]) != 1 {
			t.Fatalf("n=%d: want single root leaf, got %d levels", n, len(levels))
		}
		if l.TreeHeight() != 1 {
			t.Fatalf("n=%d height = %d", n, l.TreeHeight())
		}
	}
	t.Logf("判定: n=0/1/2 均为单叶根，高度=1，不受最小占用限制（空输入为空叶根）")
}

// TestEmptyInput: empty stream yields an empty leaf root.
func TestEmptyInput(t *testing.T) {
	l := finishLoader(t, 4, 4, 50, nil)
	ex, pages, err := l.Get("k0000")
	if err != nil || ex || pages != 1 {
		t.Fatalf("Get on empty tree = %v,%d,%v want false,1,nil", ex, pages, err)
	}
	if len(l.Levels()[0][0].Keys) != 0 {
		t.Fatalf("root not empty: %v", l.Levels()[0][0].Keys)
	}
	t.Logf("输入=<空> 输出=[空叶根] 判定: Get(k0000)=false 访问页数=1")
}

func leafSizes(pages []Page) []int {
	out := make([]int, len(pages))
	for i, pg := range pages {
		out[i] = len(pg.Keys)
	}
	return out
}

func internalSizes(pages []Page) []int {
	out := make([]int, len(pages))
	for i, pg := range pages {
		out[i] = len(pg.Children)
	}
	return out
}
