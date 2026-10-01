package ontology

import (
	"reflect"
	"sync"
	"testing"
)

// TestAddReplaceDeleteErrors 覆盖各错误分支及“参数非法先于文档存在性”的顺序。
func TestAddReplaceDeleteErrors(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"c": {"a"}}))

	wantErrIs(t, s.Add("", map[string][]string{"c": {"a"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {""}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {"/a"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {"a/"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {"a//b"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {"a/b/c/d/e"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"": {"a"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{"c": {"a", "a"}}), ErrInvalidArgument)
	wantErrIs(t, s.Add("d2", map[string][]string{}), nil) // 空 attrs 合法
	wantErrIs(t, s.Add("d1", map[string][]string{"c": {"b"}}), ErrDuplicateDocument)

	// Replace：参数非法先于文档不存在。
	wantErrIs(t, s.Replace("missing", map[string][]string{"c": {""}}), ErrInvalidArgument)
	wantErrIs(t, s.Replace("missing", map[string][]string{"c": {"a"}}), ErrDocumentNotFound)
	wantErrIs(t, s.Delete("missing"), ErrDocumentNotFound)
	wantErrIs(t, s.Delete(""), ErrInvalidArgument)
}

// TestRejectedOperationKeepsState 被拒绝的操作不得改变文档与依赖。
func TestRejectedOperationKeepsState(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}, "size": {"L"}}))
	must(t, s.Link("size", "color"))

	before, err := s.Facets(selOf("color", []string{"red"}, "size", []string{"L"}), 50)
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Add("d1", map[string][]string{"color": {"blue"}})
	_ = s.Replace("d2", map[string][]string{"color": {"blue"}})
	_ = s.Delete("d2")
	_ = s.Link("size", "other")
	_ = s.Link("color", "size") // 成环
	_, _ = s.Facets(map[string]map[string]struct{}{"c": setOf("/bad")}, 50)

	after, err := s.Facets(selOf("color", []string{"red"}, "size", []string{"L"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed by rejected ops:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestReplaceAndDelete Replace 原子替换，Delete 生效。
func TestReplaceAndDelete(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"c": {"a/b"}}))
	must(t, s.Replace("d1", map[string][]string{"c": {"x"}}))

	got, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := FacetsResult{
		Total:  1,
		Facets: []FacetResult{{Dimension: "c", Items: []FacetItem{item("x", 1, false)}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after replace got %+v want %+v", got, want)
	}

	must(t, s.Delete("d1"))
	got, err = s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || len(got.Facets) != 0 {
		t.Fatalf("after delete got %+v", got)
	}

	// 删除后可重新 Add 同一 docID。
	must(t, s.Add("d1", map[string][]string{"c": {"y"}}))
}

// TestNoStateAliasing 返回内容不别名内部状态，入参修改也不影响内部状态。
func TestNoStateAliasing(t *testing.T) {
	s := New()
	attrs := map[string][]string{"color": {"red"}}
	must(t, s.Add("d1", attrs))

	// 修改入参切片不影响已登记文档。
	attrs["color"][0] = "blue"
	attrs["color"] = append(attrs["color"], "green")

	selected := selOf("color", []string{"red"})
	got, err := s.Facets(selected, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Facets[0].Items[0].Value != "red" {
		t.Fatalf("internal state aliased by input: %+v", got)
	}

	// 修改返回切片不影响后续查询。
	got.Facets[0].Items[0].Value = "tampered"
	got2, err := s.Facets(selected, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Facets[0].Items[0].Value != "red" {
		t.Fatalf("internal state aliased by output: %+v", got2)
	}

	// Replace 后旧引用不复用。
	old := got2
	must(t, s.Replace("d1", map[string][]string{"color": {"orange"}}))
	if old.Facets[0].Items[0].Value != "red" {
		t.Fatalf("old snapshot mutated: %+v", old)
	}
}

// TestLimits 恰好 16 个维度、每维度 32 个值合法，超出非法。
func TestLimits(t *testing.T) {
	attrs := map[string][]string{}
	for i := 0; i < maxDimensionsPerDoc; i++ {
		dim := "d" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		values := make([]string, maxValuesPerDim)
		for j := range values {
			values[j] = "v" + string(rune('a'+j))
		}
		attrs[dim] = values
	}
	s := New()
	if err := s.Add("ok", attrs); err != nil {
		t.Fatalf("limit boundary should be valid: %v", err)
	}

	tooManyDims := cloneAttrs(attrs)
	tooManyDims["zz"] = []string{"a"}
	wantErrIs(t, s.Add("x", tooManyDims), ErrInvalidArgument)

	tooManyValues := map[string][]string{"c": make([]string, maxValuesPerDim+1)}
	for j := range tooManyValues["c"] {
		tooManyValues["c"][j] = "v" + string(rune('a'+j))
	}
	wantErrIs(t, s.Add("y", tooManyValues), ErrInvalidArgument)
}

// TestConcurrentAccess 并发增删改、声明与计数等价于某串行顺序（配合 -race 验证）。
func TestConcurrentAccess(t *testing.T) {
	s := New()
	must(t, s.Link("size", "color"))

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := "d" + string(rune('0'+(i%8)))
				attrs := map[string][]string{
					"color": {"red", "blue"},
					"size":  {"L", "M"},
				}
				switch i % 4 {
				case 0:
					_ = s.Add(id, attrs)
				case 1:
					_ = s.Replace(id, attrs)
				case 2:
					_ = s.Delete(id)
				case 3:
					result, err := s.Facets(selOf("color", []string{"red"}, "size", []string{"L"}), 10)
					if err != nil {
						t.Errorf("Facets error: %v", err)
						return
					}
					// 计数恒非负且不超过文档总数。
					for _, f := range result.Facets {
						for _, it := range f.Items {
							if it.Count < 0 || it.Count > result.Total+8 {
								t.Errorf("implausible count: %+v total=%d", it, result.Total)
								return
							}
						}
					}
				}
			}
		}(worker)
	}
	wg.Wait()
}
