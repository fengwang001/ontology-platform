package ontology

import (
	"reflect"
	"testing"
)

func setOf(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

// TestLinkRejections 重复声明、自环与二级环被拒，且拒绝不改变已有依赖。
func TestLinkRejections(t *testing.T) {
	s := New()
	must(t, s.Link("b", "a"))

	wantErrIs(t, s.Link("b", "a"), ErrDuplicateLink)
	wantErrIs(t, s.Link("b", "c"), ErrDuplicateLink)
	wantErrIs(t, s.Link("a", "a"), ErrCyclicDependency)

	// 二级环：c -> b -> a，再 a -> c 成环。
	must(t, s.Link("c", "b"))
	wantErrIs(t, s.Link("a", "c"), ErrCyclicDependency)

	// 空名参数非法。
	wantErrIs(t, s.Link("", "a"), ErrInvalidArgument)
	wantErrIs(t, s.Link("a", ""), ErrInvalidArgument)

	// 依赖仍为 b->a、c->b，a 无 parent；用实际文档验证生效链未被破坏。
	must(t, s.Add("d1", map[string][]string{"a": {"x"}, "b": {"y"}, "c": {"z"}}))
	if got, err := s.Facets(map[string]map[string]struct{}{}, 1); err != nil {
		t.Fatal(err)
	} else if len(got.Facets) != 1 || got.Facets[0].Dimension != "a" {
		t.Fatalf("after rejections only a should be root-active, got %+v", got.Facets)
	}
	if got, err := s.Facets(selOf("a", []string{"x"}, "b", []string{"y"}), 1); err != nil {
		t.Fatal(err)
	} else if len(got.Facets) != 3 {
		t.Fatalf("chain should remain intact, got %+v", got.Facets)
	}
}

// TestTieBreakByteOrder count 并列时按值字节序升序。
func TestTieBreakByteOrder(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"c": {"z", "m", "a"}}))

	got, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	gotItems := got.Facets[0].Items
	wantItems := []FacetItem{
		item("a", 1, false),
		item("m", 1, false),
		item("z", 1, false),
	}
	if !reflect.DeepEqual(gotItems, wantItems) {
		t.Fatalf("items=%+v want %+v", gotItems, wantItems)
	}
}

// TestTopNTruncation topN 恰等于候选数与少 1。
func TestTopNTruncation(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"c": {"a", "b", "c"}}))

	full, err := s.Facets(map[string]map[string]struct{}{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Facets[0].Items) != 3 {
		t.Fatalf("topN==candidates: %+v", full.Facets[0].Items)
	}

	short, err := s.Facets(map[string]map[string]struct{}{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if items := short.Facets[0].Items; !reflect.DeepEqual(items, []FacetItem{
		item("a", 1, false),
		item("b", 1, false),
	}) {
		t.Fatalf("topN=candidates-1: %+v", items)
	}

	_, err0 := s.Facets(map[string]map[string]struct{}{}, 0)
	wantErrIs(t, err0, ErrInvalidArgument)
	_, err51 := s.Facets(map[string]map[string]struct{}{}, 51)
	wantErrIs(t, err51, ErrInvalidArgument)
}

// TestDimensionWithoutCandidatesOmitted 没有候选值的生效维度不出现在结果中。
func TestDimensionWithoutCandidatesOmitted(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}}))
	must(t, s.Link("size", "color"))

	// color 已选非空使 size 生效，但任何文档都没有 size 值：无候选值，不出现在结果中。
	got, err := s.Facets(selOf("color", []string{"red"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Facets) != 1 || got.Facets[0].Dimension != "color" {
		t.Fatalf("facets=%+v want color only", got.Facets)
	}
}

// TestZeroCountSelectedAppearsAndRanks 已选值计数为 0 仍出现，参与排序与截断。
func TestZeroCountSelectedAppearsAndRanks(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"c": {"a"}, "x": {"1"}}))
	must(t, s.Add("d2", map[string][]string{"c": {"b"}, "x": {"1"}}))

	// x=1 不缩小集合；c 已选 "zz"（计数 0）。候选 a=1,b=1,zz=0。
	got, err := s.Facets(selOf("x", []string{"1"}, "c", []string{"zz"}), 2)
	if err != nil {
		t.Fatal(err)
	}
	cResult := findFacet(t, got, "c")
	if !reflect.DeepEqual(cResult.Items, []FacetItem{
		item("a", 1, false),
		item("b", 1, false),
	}) {
		t.Fatalf("topN=2 items: %+v", cResult.Items)
	}

	got, err = s.Facets(selOf("x", []string{"1"}, "c", []string{"zz"}), 3)
	if err != nil {
		t.Fatal(err)
	}
	cResult = findFacet(t, got, "c")
	if !reflect.DeepEqual(cResult.Items, []FacetItem{
		item("a", 1, false),
		item("b", 1, false),
		item("zz", 0, true),
	}) {
		t.Fatalf("topN=3 items: %+v", cResult.Items)
	}
}

// TestSelectedDimensionInNoDocument 已选维度不存在于任何文档：Total=0，该维度仍以计数 0 的已选值出现。
func TestSelectedDimensionInNoDocument(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}}))

	got, err := s.Facets(selOf("missing", []string{"x/y"}, "color", []string{"red"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 {
		t.Fatalf("Total=%d want 0", got.Total)
	}
	want := FacetsResult{
		Total: 0,
		Facets: []FacetResult{
			{Dimension: "color", Items: []FacetItem{item("red", 0, true)}},
			{Dimension: "missing", Items: []FacetItem{item("x/y", 0, true)}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// TestDependencyChainTwoLevels 两级依赖链：祖先已选集合为空使整条后代链不生效。
func TestDependencyChainTwoLevels(t *testing.T) {
	s := New()
	must(t, s.Link("region", "country"))
	must(t, s.Link("city", "region"))
	must(t, s.Add("d1", map[string][]string{
		"country": {"cn"},
		"region":  {"north"},
		"city":    {"beijing"},
	}))

	// 无已选：仅 country 生效。
	got, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Facets) != 1 || got.Facets[0].Dimension != "country" {
		t.Fatalf("facets=%+v want only country", got.Facets)
	}

	// country 已选非空后 region 生效；region 已选空，city 仍不生效。
	got, err = s.Facets(selOf("country", []string{"cn"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	dims := map[string]bool{}
	for _, f := range got.Facets {
		dims[f.Dimension] = true
	}
	if !dims["country"] || !dims["region"] || dims["city"] {
		t.Fatalf("dims=%v want {country, region} without city", dims)
	}

	// region 也已选非空后，city 才生效。
	got, err = s.Facets(selOf("country", []string{"cn"}, "region", []string{"north"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	dims = map[string]bool{}
	for _, f := range got.Facets {
		dims[f.Dimension] = true
	}
	if !dims["city"] {
		t.Fatalf("dims=%v want city active", dims)
	}
}

// TestChildActiveWhenParentSelectionMatchesNothing parent 已选非空但无文档匹配时，child 仍生效。
func TestChildActiveWhenParentSelectionMatchesNothing(t *testing.T) {
	s := New()
	must(t, s.Link("size", "color"))
	must(t, s.Add("d1", map[string][]string{"color": {"red"}, "size": {"L"}}))

	got, err := s.Facets(selOf("color", []string{"purple"}, "size", []string{"L"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	dims := map[string]bool{}
	for _, f := range got.Facets {
		dims[f.Dimension] = true
	}
	if !dims["size"] {
		t.Fatalf("size should stay active: dims=%v", dims)
	}
	if got.Total != 0 {
		t.Fatalf("Total=%d want 0", got.Total)
	}
	sizeItems := findFacet(t, got, "size").Items
	if !reflect.DeepEqual(sizeItems, []FacetItem{item("L", 0, true)}) {
		t.Fatalf("size items=%+v", sizeItems)
	}
}

// TestOwnSelectionKeepsOtherCounts 本维度已选时，其他值计数不被本维度过滤压成 0。
func TestOwnSelectionKeepsOtherCounts(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}}))
	must(t, s.Add("d2", map[string][]string{"color": {"blue"}}))

	got, err := s.Facets(selOf("color", []string{"red"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	want := FacetsResult{
		Total: 1,
		Facets: []FacetResult{{
			Dimension: "color",
			Items: []FacetItem{
				item("blue", 1, false),
				item("red", 1, true),
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// TestOtherDimensionNarrowsCounts 他维度已选时，本维度计数随之收缩。
func TestOtherDimensionNarrowsCounts(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}, "size": {"L"}}))
	must(t, s.Add("d2", map[string][]string{"color": {"red"}, "size": {"M"}}))

	got, err := s.Facets(selOf("size", []string{"L"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	want := FacetsResult{
		Total: 1,
		Facets: []FacetResult{
			{
				Dimension: "color",
				Items:     []FacetItem{item("red", 1, false)},
			},
			{
				Dimension: "size",
				Items: []FacetItem{
					item("L", 1, true),
					item("M", 1, false),
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// TestEmptySelectionEquivalentToAbsent 已选集合为空与不出现该维度等价。
func TestEmptySelectionEquivalentToAbsent(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"color": {"red"}}))

	got1, err := s.Facets(map[string]map[string]struct{}{"color": {}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("empty set %+v != absent %+v", got1, got2)
	}
}

// TestHierarchicalPrefixMatching 选父路径匹配其后代，而不匹配仅共享字符串前缀的路径。
func TestHierarchicalPrefixMatching(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"p": {"a/b/c"}}))
	must(t, s.Add("d2", map[string][]string{"p": {"ab"}}))
	must(t, s.Add("d3", map[string][]string{"p": {"a/bc"}}))

	// 选 "a"：d1(a/b/c) 与 d3(a/bc) 按段前缀命中，d2(ab) 仅共享字符串前缀而不命中。
	got, err := s.Facets(selOf("p", []string{"a"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 {
		t.Fatalf("select a: Total=%d want 2", got.Total)
	}
	if items := got.Facets[0].Items; !reflect.DeepEqual(items, []FacetItem{
		item("a", 2, true),
		item("a/b", 1, false),
		item("a/b/c", 1, false),
		item("a/bc", 1, false),
		item("ab", 1, false),
	}) {
		t.Fatalf("select a items: %+v", items)
	}

	// 选 "a/b"：同样只命中 d1，"a/bc" 不是 "a/b" 的段前缀后代。
	got, err = s.Facets(selOf("p", []string{"a/b"}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Fatalf("select a/b: Total=%d want 1", got.Total)
	}
}

// TestSharedAncestorCountedOnce 同一文档多值共享祖先时祖先只计一次。
func TestSharedAncestorCountedOnce(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{"p": {"a/b/c", "a/d"}}))

	got, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, it := range got.Facets[0].Items {
		counts[it.Value] = it.Count
	}
	wantCounts := map[string]int{"a": 1, "a/b": 1, "a/b/c": 1, "a/d": 1}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("counts=%v want %v", counts, wantCounts)
	}
}

func selOf(pairs ...interface{}) map[string]map[string]struct{} {
	selected := make(map[string]map[string]struct{})
	for i := 0; i < len(pairs); i += 2 {
		selected[pairs[i].(string)] = setOf(pairs[i+1].([]string)...)
	}
	return selected
}

func item(value string, count int, selected bool) FacetItem {
	return FacetItem{Value: value, Count: count, Selected: selected}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErrIs(t *testing.T, got, target error) {
	t.Helper()
	if got != target {
		t.Fatalf("error = %v, want %v", got, target)
	}
}

func findFacet(t *testing.T, result FacetsResult, dim string) FacetResult {
	t.Helper()
	for _, f := range result.Facets {
		if f.Dimension == dim {
			return f
		}
	}
	t.Fatalf("dimension %q not in result %+v", dim, result)
	return FacetResult{}
}

// TestSpecExample 覆盖题面给出的三文档例子及其三次 Facets 查询。
func TestSpecExample(t *testing.T) {
	s := New()
	must(t, s.Add("d1", map[string][]string{
		"color": {"red/dark"},
		"size":  {"L"},
	}))
	must(t, s.Add("d2", map[string][]string{
		"color": {"red/light", "blue"},
		"size":  {"M"},
	}))
	must(t, s.Add("d3", map[string][]string{
		"color": {"blue/navy"},
		"size":  {"L"},
	}))
	must(t, s.Link("size", "color"))

	got, err := s.Facets(map[string]map[string]struct{}{}, 50)
	if err != nil {
		t.Fatalf("Facets({}) error: %v", err)
	}
	want := FacetsResult{
		Total: 3,
		Facets: []FacetResult{{
			Dimension: "color",
			Items: []FacetItem{
				item("blue", 2, false),
				item("red", 2, false),
				item("blue/navy", 1, false),
				item("red/dark", 1, false),
				item("red/light", 1, false),
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Facets({})\n got: %+v\nwant: %+v", got, want)
	}

	got, err = s.Facets(selOf("color", []string{"red"}, "size", []string{"L"}), 50)
	if err != nil {
		t.Fatalf("Facets red+L error: %v", err)
	}
	want = FacetsResult{
		Total: 1,
		Facets: []FacetResult{
			{
				Dimension: "color",
				Items: []FacetItem{
					item("blue", 1, false),
					item("blue/navy", 1, false),
					item("red", 1, true),
					item("red/dark", 1, false),
				},
			},
			{
				Dimension: "size",
				Items: []FacetItem{
					item("L", 1, true),
					item("M", 1, false),
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Facets red+L\n got: %+v\nwant: %+v", got, want)
	}

	// 仅选 size 时，color 已选为空导致 size 不生效，Total 为 3。
	got, err = s.Facets(selOf("size", []string{"L"}), 50)
	if err != nil {
		t.Fatalf("Facets size-only error: %v", err)
	}
	want = FacetsResult{
		Total: 3,
		Facets: []FacetResult{{
			Dimension: "color",
			Items: []FacetItem{
				item("blue", 2, false),
				item("red", 2, false),
				item("blue/navy", 1, false),
				item("red/dark", 1, false),
				item("red/light", 1, false),
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Facets size-only\n got: %+v\nwant: %+v", got, want)
	}
}
