package grant

import (
	"fmt"
	"testing"

	"ontology/territory"
)

// buildWideTree 构造深度不超过 5、叶数 ≥ nLeaves 的宽扇出树（代码按字节序编号）。
func buildWideTree(t *testing.T, nLeaves int) *territory.Tree {
	t.Helper()
	kids := map[string][]string{}
	// 每层扇出：depth 0->1 用 2，其下按需要分扇出，保证 5 层内达到 nLeaves。
	factors := []int{2, 7, 7, 7, 15}
	leafCount := 1
	for _, f := range factors {
		leafCount *= f
	}
	if leafCount < nLeaves {
		t.Fatalf("layout capacity %d < requested %d", leafCount, nLeaves)
	}
	// 逐层生成：nodes[d] 是该层节点代码（d=0 为根）
	levels := [][]string{{territory.Root}}
	for d, fan := range factors {
		var cur []string
		for _, parent := range levels[d] {
			row := make([]string, fan)
			for k := 0; k < fan; k++ {
				code := fmt.Sprintf("n%d_%s_%02d", d+1, parent, k)
				row[k] = code
				cur = append(cur, code)
			}
			kids[parent] = row
		}
		levels = append(levels, cur)
	}
	tr, err := territory.NewTree(kids)
	if err != nil {
		t.Fatalf("NewTree leaves>=%d: %v", nLeaves, err)
	}
	return tr
}

// TestSpanCounterIndependentOfLeafCount 证明：求公共叶时触碰区间数
// 只取决于双方排除数，与树中叶总数无关（100 与 10000 两档必须相等）。
func TestSpanCounterIndependentOfLeafCount(t *testing.T) {
	type measurement struct {
		spans int
	}
	measure := func(tr *territory.Tree, nExclude int) measurement {
		leaves := tr.Leaves()
		r := NewRegistry(tr)
		// gA: WORLD 排除前 nExclude 个叶（随机散布更能代表“挖洞”）
		exA := pickLeaves(leaves, nExclude, false)
		gA := r.mustAdd(t, addArgs{now: 1, id: "a", title: "T", licensee: "A",
			node: territory.Root, excludes: exA, start: 0, end: 100, exclusive: true})
		exB := pickLeaves(leaves, nExclude, true)
		gB := &Grant{
			ID:        bs("b"),
			Title:     bs("T"),
			Licensee:  bs("B"),
			Start:     0,
			End:       100,
			Exclusive: true,
			cover:     mustCover(tr, territory.Root, exB),
		}
		r.resetCounters()
		r.spansOverlap(gA.cover, gB.cover)
		s := r.stats()
		if s.Spans > 2*nExclude+2 {
			t.Fatalf("spans=%d exceeds bound %d", s.Spans, 2*nExclude+2)
		}
		return measurement{spans: s.Spans}
	}
	for _, nEx := range []int{0, 1, 4, 8} {
		small := measure(buildWideTree(t, 100), nEx)
		large := measure(buildWideTree(t, 10_000), nEx)
		if small != large {
			t.Fatalf("nExclude=%d: spans(100 leaves)=%d but spans(10000 leaves)=%d",
				nEx, small.spans, large.spans)
		}
		t.Logf("nExclude=%d: touched spans=%d (bound %d), identical at 100 and 10000 leaves",
			nEx, small.spans, 2*nEx+2)
	}
}

func mustCover(tr *territory.Tree, node string, excludes []string) []territory.Span {
	keep, _ := tr.Cover(node, excludes)
	if len(keep) == 0 {
		panic("empty cover in counter test")
	}
	return keep
}

// pickLeaves 均匀选取 n 个叶，offset 保证两次选择不同但都使覆盖非空。
func pickLeaves(leaves []string, n int, odd bool) []string {
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	step := len(leaves) / (2*n + 1)
	for i := 0; i < n; i++ {
		idx := ((2*i + 1) * step) % len(leaves)
		if odd {
			idx = ((2*i + 2) * step) % len(leaves)
		}
		out = append(out, leaves[idx])
	}
	return out
}

// TestComparedCounterIgnoresOtherTitles 证明一次 Add 的比较次数只等于
// 同一 title 下的授权数，与其他 title 总量无关（100 与 10000 两档相等）。
func TestComparedCounterIgnoresOtherTitles(t *testing.T) {
	measure := func(tr *territory.Tree, otherTitles int) int {
		leaves := tr.Leaves()
		r := NewRegistry(tr)
		for i := 0; i < otherTitles; i++ {
			title := fmt.Sprintf("other-%05d", i)
			r.mustAdd(t, addArgs{now: 1, id: fmt.Sprintf("o-%05d", i), title: title,
				licensee: "X", node: leaves[i%len(leaves)], start: 0, end: 1000, exclusive: true})
		}
		const sameTitle = 3
		for i := 0; i < sameTitle; i++ {
			r.mustAdd(t, addArgs{now: 1, id: fmt.Sprintf("t-%d", i), title: "T",
				licensee: fmt.Sprintf("L%d", i), node: leaves[5],
				start: 0, end: 10, exclusive: false})
		}
		r.resetCounters()
		_, err := r.Add(1, bs("new"), bs("T"), bs("NEW"), leaves[5], nil, 0, 5, true)
		if err == nil {
			t.Fatal("new exclusive must conflict with same-title non-exclusive grants")
		}
		got := r.stats().Compared
		if got != sameTitle {
			t.Fatalf("compared=%d, want exactly %d (same-title grants)", got, sameTitle)
		}
		return got
	}
	tr := buildWideTree(t, 10_000)
	a := measure(tr, 100)
	b := measure(tr, 10_000)
	if a != b {
		t.Fatalf("compared differs: 100 other titles -> %d, 10000 -> %d", a, b)
	}
	t.Logf("compared=%d with both 100 and 10000 other-title grants", a)
}
