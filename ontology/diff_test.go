package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func sortStrings(values []string) { sort.Strings(values) }

func sortStringsByCount(values []string, counts map[string]int) {
	sort.Slice(values, func(i, j int) bool {
		if counts[values[i]] != counts[values[j]] {
			return counts[values[i]] > counts[values[j]]
		}
		return values[i] < values[j]
	})
}

const randomDiffIterations = 2000

// randPath 生成 1~4 段的随机路径，段名取自一个小词表，
// 这样既能制造共享前缀（a、a/b、a/bc 等），又能制造字符串前缀巧合。
func randPath(rng *rand.Rand) string {
	segments := []string{"a", "ab", "b", "bc", "c", "red", "redx", "l", "l2"}
	n := 1 + rng.Intn(4)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = segments[rng.Intn(len(segments))]
	}
	return strings.Join(parts, "/")
}

func randAttrs(rng *rand.Rand) map[string][]string {
	dimNames := []string{"color", "size", "shape", "region"}
	nDims := rng.Intn(4) // 0..3 个维度（含空映射）
	rng.Shuffle(len(dimNames), func(i, j int) { dimNames[i], dimNames[j] = dimNames[j], dimNames[i] })
	attrs := map[string][]string{}
	for i := 0; i < nDims; i++ {
		dim := dimNames[i]
		nValues := 1 + rng.Intn(4)
		seen := map[string]struct{}{}
		values := make([]string, 0, nValues)
		for len(values) < nValues {
			value := randPath(rng)
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		attrs[dim] = values
	}
	return attrs
}

func formatAttrs(attrs map[string][]string) string {
	dims := make([]string, 0, len(attrs))
	for dim := range attrs {
		dims = append(dims, dim)
	}
	sort.Strings(dims)
	parts := make([]string, 0, len(dims))
	for _, dim := range dims {
		parts = append(parts, fmt.Sprintf("%s=%v", dim, attrs[dim]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatSelected(selected map[string]map[string]struct{}) string {
	dims := make([]string, 0, len(selected))
	for dim := range selected {
		dims = append(dims, dim)
	}
	sort.Strings(dims)
	parts := make([]string, 0, len(dims))
	for _, dim := range dims {
		values := make([]string, 0, len(selected[dim]))
		for value := range selected[dim] {
			values = append(values, value)
		}
		sort.Strings(values)
		parts = append(parts, fmt.Sprintf("%s=%v", dim, values))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatFacets(result FacetsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Total=%d", result.Total)
	for _, f := range result.Facets {
		fmt.Fprintf(&b, " %s:[", f.Dimension)
		for i, it := range f.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			mark := ""
			if it.Selected {
				mark = "*"
			}
			fmt.Fprintf(&b, "%s%s=%d", it.Value, mark, it.Count)
		}
		b.WriteString("]")
	}
	return b.String()
}

// TestRandomDifferentialAgainstNaive 对拍 2000 组随机操作序列：
// 增量 Store 的输出必须与逐文档重判的朴素实现在每一步完全一致。
// 失败与首个分歧处打印输入、输出与判定依据。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	if !testing.Verbose() {
		t.Log("differential test runs regardless; run with -v to see each iteration")
	}
	rng := rand.New(rand.NewSource(20261001))

	for iter := 0; iter < randomDiffIterations; iter++ {
		store := New()
		naive := newNaiveStore()
		var log strings.Builder

		// 每个序列固定使用一组 docID 与可能的 Link，制造重复添加、替换、删除。
		nOps := 2 + rng.Intn(12)
		for op := 0; op < nOps; op++ {
			switch rng.Intn(10) {
			case 0, 1, 2: // Add
				id := fmt.Sprintf("d%d", rng.Intn(6))
				attrs := randAttrs(rng)
				got := store.Add(id, attrs)
				if _, exists := naive.docs[id]; exists {
					if got != ErrDuplicateDocument {
						t.Fatalf("iter=%d Add(%s) err=%v want ErrDuplicateDocument\n%s", iter, id, got, log.String())
					}
					fmt.Fprintf(&log, "Add(%s, %s) -> duplicate (state unchanged)\n", id, formatAttrs(attrs))
				} else {
					if got != nil {
						t.Fatalf("iter=%d Add(%s) unexpected err=%v\n%s", iter, id, got, log.String())
					}
					naive.docs[id] = cloneAttrs(attrs)
					fmt.Fprintf(&log, "Add(%s, %s) -> ok\n", id, formatAttrs(attrs))
				}
			case 3, 4: // Replace
				id := fmt.Sprintf("d%d", rng.Intn(6))
				attrs := randAttrs(rng)
				got := store.Replace(id, attrs)
				if _, exists := naive.docs[id]; !exists {
					if got != ErrDocumentNotFound {
						t.Fatalf("iter=%d Replace(%s) err=%v want ErrDocumentNotFound\n%s", iter, id, got, log.String())
					}
					fmt.Fprintf(&log, "Replace(%s, %s) -> not found\n", id, formatAttrs(attrs))
				} else {
					if got != nil {
						t.Fatalf("iter=%d Replace(%s) unexpected err=%v\n%s", iter, id, got, log.String())
					}
					naive.docs[id] = cloneAttrs(attrs)
					fmt.Fprintf(&log, "Replace(%s, %s) -> ok\n", id, formatAttrs(attrs))
				}
			case 5: // Delete
				id := fmt.Sprintf("d%d", rng.Intn(6))
				got := store.Delete(id)
				if _, exists := naive.docs[id]; !exists {
					if got != ErrDocumentNotFound {
						t.Fatalf("iter=%d Delete(%s) err=%v want ErrDocumentNotFound\n%s", iter, id, got, log.String())
					}
					fmt.Fprintf(&log, "Delete(%s) -> not found\n", id)
				} else {
					if got != nil {
						t.Fatalf("iter=%d Delete(%s) unexpected err=%v\n%s", iter, id, got, log.String())
					}
					delete(naive.docs, id)
					fmt.Fprintf(&log, "Delete(%s) -> ok\n", id)
				}
			case 6: // Link
				child := []string{"size", "shape", "region", "color"}[rng.Intn(4)]
				parent := []string{"size", "shape", "region", "color"}[rng.Intn(4)]
				got := store.Link(child, parent)
				expected := func() error {
					if _, ok := naive.parent[child]; ok {
						return ErrDuplicateLink
					}
					if child == parent {
						return ErrCyclicDependency
					}
					cur := parent
					for {
						if cur == child {
							return ErrCyclicDependency
						}
						next, ok := naive.parent[cur]
						if !ok {
							return nil
						}
						cur = next
					}
				}()
				if got != expected {
					t.Fatalf("iter=%d Link(%s,%s) err=%v want %v\n%s", iter, child, parent, got, expected, log.String())
				}
				if expected == nil {
					naive.parent[child] = parent
				}
				fmt.Fprintf(&log, "Link(%s, %s) -> %v\n", child, parent, got)
			default: // Facets
				selected := map[string]map[string]struct{}{}
				dims := []string{"color", "size", "shape", "region", "ghost"}
				for _, dim := range dims {
					switch rng.Intn(3) {
					case 0:
						// 不出现
					case 1:
						selected[dim] = map[string]struct{}{} // 空集合
					case 2:
						nValues := 1 + rng.Intn(3)
						set := map[string]struct{}{}
						for j := 0; j < nValues; j++ {
							set[randPath(rng)] = struct{}{}
						}
						selected[dim] = set
					}
				}
				topN := 1 + rng.Intn(50)
				got, err := store.Facets(selected, topN)
				if err != nil {
					t.Fatalf("iter=%d Facets unexpected err=%v\n%s", iter, err, log.String())
				}
				want := naive.facets(selected, topN)
				input := fmt.Sprintf("Facets(selected=%s, topN=%d)", formatSelected(selected), topN)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("iter=%d MISMATCH\ninput: %s\nincremental: %s\nnaive      : %s\n判定依据: 朴素实现对每个生效维度逐文档重算层级节点集合与交集；active 沿 parent 链递归且要求 parent 已选非空；count(d,v) 跳过 d 自身约束\noperations:\n%s",
						iter, input, formatFacets(got), formatFacets(want), log.String())
				}
				fmt.Fprintf(&log, "%s -> %s\n", input, formatFacets(got))
			}
		}
		if testing.Verbose() {
			t.Logf("iter=%d 输入/输出/判定:\n%s", iter, log.String())
		}
	}
}
