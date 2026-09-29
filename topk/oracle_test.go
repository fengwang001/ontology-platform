package topk

import (
	"sort"
	"testing"
)

// naiveOrdered 用最朴素的全量拷贝 + 全量排序重算完整有序序列，
// 作为判定维护器输出是否正确的参考基准（oracle）。
// 判定依据：分数降序，分数相同则标识字典序升序。
func naiveOrdered(scores map[string]int64) []Element {
	elems := make([]Element, 0, len(scores))
	for id, score := range scores {
		elems = append(elems, Element{ID: id, Score: score})
	}
	sort.Slice(elems, func(i, j int) bool {
		if elems[i].Score != elems[j].Score {
			return elems[i].Score > elems[j].Score
		}
		return elems[i].ID < elems[j].ID
	})
	return elems
}

// naiveTop 返回 oracle 序列的前 n 名（不足返回全部）。
func naiveTop(scores map[string]int64, n int) []Element {
	all := naiveOrdered(scores)
	if n < len(all) {
		all = all[:n]
	}
	return all
}

// logState 打印操作、当前有序序列与判定依据。
func logState(t *testing.T, op string, m *Maintainer) {
	t.Helper()
	ordered := m.Ordered()
	top := m.TopK()
	t.Logf("op=%-24s len=%-2d ordered=%v topK=%v | 判定依据: 分数降序, 同分按ID字典序升序", op, m.Len(), ordered, top)
}

// assertAgainstOracle 以朴素全量排序结果核对维护器的完整序列与前 K 名。
func assertAgainstOracle(t *testing.T, m *Maintainer, scores map[string]int64) {
	t.Helper()
	wantAll := naiveOrdered(scores)
	wantTop := naiveTop(scores, m.k)
	gotAll := m.Ordered()
	gotTop := m.TopK()
	if !elementsEqual(gotAll, wantAll) {
		t.Fatalf("Ordered 与朴素排序不一致:\n got=%v\nwant=%v", gotAll, wantAll)
	}
	if !elementsEqual(gotTop, wantTop) {
		t.Fatalf("TopK 与朴素排序前 %d 名不一致:\n got=%v\nwant=%v", m.k, gotTop, wantTop)
	}
	if m.Len() != len(scores) {
		t.Fatalf("Len=%d, 朴素计数=%d", m.Len(), len(scores))
	}
}

// assertPrefixes 核对任意更小的 K 取值都是完整有序序列的前缀。
func assertPrefixes(t *testing.T, m *Maintainer) {
	t.Helper()
	all := m.Ordered()
	for n := 1; n <= m.k; n++ {
		got, err := m.Top(n)
		if err != nil {
			t.Fatalf("Top(%d) 意外报错: %v", n, err)
		}
		wantLen := n
		if n > len(all) {
			wantLen = len(all)
		}
		want := append([]Element(nil), all[:wantLen]...)
		if !elementsEqual(got, want) {
			t.Fatalf("Top(%d) 不是完整序列前缀:\n got=%v\nwant=%v", n, got, want)
		}
	}
}

func elementsEqual(a, b []Element) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cloneScores 复制参考模型，保证失败用例前后对比不受原地修改影响。
func cloneScores(src map[string]int64) map[string]int64 {
	dst := make(map[string]int64, len(src))
	for id, score := range src {
		dst[id] = score
	}
	return dst
}
