package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustIndex(t *testing.T, e *QueryExpander, docID string, terms ...string) {
	t.Helper()
	if err := e.Index(docID, terms); err != nil {
		t.Fatalf("Index(%q, %v): %v", docID, terms, err)
	}
}

func mustExpand(t *testing.T, e *QueryExpander, query string, maxExp int) ExpandResult {
	t.Helper()
	result, err := e.Expand(query, maxExp)
	if err != nil {
		t.Fatalf("Expand(%q, %d): %v", query, maxExp, err)
	}
	return result
}

func assertResult(t *testing.T, got ExpandResult, want ExpandResult, reason string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %+v, want %+v", reason, got, want)
	}
}

func assertCandidate(t *testing.T, got Candidate, want Candidate, reason string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %+v, want %+v", reason, got, want)
	}
}

func TestTrailingSpaceDistinguishesCompleteWordAndPrefix(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "apple", "apply")
	mustIndex(t, e, "d2", "apple", "ape")

	withoutTrailing := mustExpand(t, e, "ap", 10)
	assertResult(t, withoutTrailing, ExpandResult{
		Candidates: []Candidate{
			{Term: "apple", ConditionalDF: 2, Score: 2},
			{Term: "ape", ConditionalDF: 1, Score: 1},
			{Term: "apply", ConditionalDF: 1, Score: 1},
		},
		Hits: []string{"d1", "d2"},
	}, "末词不带空格时是前缀词")

	withTrailing := mustExpand(t, e, "ap ", 10)
	assertResult(t, withTrailing, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{},
	}, "末词带空格时是完整词；词典中没有 ap")

	withManySpaces := mustExpand(t, e, "  apple   ape  ", 10)
	assertResult(t, withManySpaces, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{"d2"},
	}, "连续空格折叠，查询以空格结尾时所有词均为完整词")
}

func TestPrefixEqualsTermAndTruncationBoundary(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "a", "ab")
	mustIndex(t, e, "d2", "a")

	got := mustExpand(t, e, "a", 2)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{
			{Term: "a", ConditionalDF: 2, Score: 2},
			{Term: "ab", ConditionalDF: 1, Score: 1},
		},
		Hits: []string{"d1", "d2"},
	}, "maxExp 恰等于候选数时不截断")

	got = mustExpand(t, e, "a", 1)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{{Term: "a", ConditionalDF: 2, Score: 2}},
		Truncated:  true,
		Hits:       []string{"d1", "d2"},
	}, "maxExp 比候选数少 1 时截断")
}

func TestConditionalDFDiffersFromGlobalDF(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "anchor", "pz")
	mustIndex(t, e, "d2", "anchor", "pa")
	mustIndex(t, e, "d3", "anchor", "pa")
	mustIndex(t, e, "d4", "pb")
	mustIndex(t, e, "d5", "pb")
	mustIndex(t, e, "d6", "pb")

	got := mustExpand(t, e, "anchor p", 1)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{{Term: "pa", ConditionalDF: 2, Score: 2}},
		Truncated:  true,
		Hits:       []string{"d2", "d3"},
	}, "入选按条件 df 而非全局 df")
}

func TestZeroConditionalDFDoesNotCountOrTruncate(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "anchor", "pea")
	mustIndex(t, e, "d2", "pear")

	got := mustExpand(t, e, "anchor pe", 1)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{{Term: "pea", ConditionalDF: 1, Score: 1}},
		Hits:       []string{"d1"},
	}, "条件 df 为 0 的 pear 不是候选，也不占用名额")
}

func TestTieBreaksByByteOrder(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "B", "zebra", "common")
	mustIndex(t, e, "A", "apple", "common")

	got := mustExpand(t, e, "common ", 10)
	if !reflect.DeepEqual(got.Hits, []string{"A", "B"}) {
		t.Fatalf("docID 按字节序排序: got %v", got.Hits)
	}

	mustIndex(t, e, "d1", "pA")
	mustIndex(t, e, "d2", "pB")
	got = mustExpand(t, e, "p", 10)
	if len(got.Candidates) != 2 || got.Candidates[0].Term != "pA" || got.Candidates[1].Term != "pB" {
		t.Fatalf("得分并列应按词项字节序升序: got %v", got.Candidates)
	}
}

func TestPickWindowAndDeduplication(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "x", "y")

	first := mustExpand(t, e, "x", 10)
	assertCandidate(t, first.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 1},
		"Pick 前的 Expand 不获得加成")

	if err := e.Pick("x"); err != nil {
		t.Fatal(err)
	}

	second := mustExpand(t, e, "x", 10)
	assertCandidate(t, second.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 3},
		"Pick 之后第 1 次 Expand 有效")

	if err := e.Pick("x"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("x"); err != nil {
		t.Fatal(err)
	}

	third := mustExpand(t, e, "x", 10)
	assertCandidate(t, third.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 5},
		"T2 重复 Pick 只计一次；T3 时 c=1 与 c=2 两条记录有效")

	if err := e.Pick("x"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("x"); err != nil {
		t.Fatal(err)
	}

	fourth := mustExpand(t, e, "x", 10)
	assertCandidate(t, fourth.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 7},
		"T3 与 T4 前的 Pick 属于不同 c；T4 时三条窗口记录都有效")

	fifth := mustExpand(t, e, "x", 10)
	assertCandidate(t, fifth.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 5},
		"T5 时最早记录 t-c=4 已失效，其余两条仍有效")

	sixth := mustExpand(t, e, "x", 10)
	assertCandidate(t, sixth.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 3},
		"T6 时只剩一条 t-c=3 的记录")

	seventh := mustExpand(t, e, "x", 10)
	assertCandidate(t, seventh.Candidates[0], Candidate{Term: "x", ConditionalDF: 1, Score: 1},
		"T7 时最后一条旧记录 t-c=4 失效")
}

func TestRejectedExpandDoesNotIncrementT(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "term")
	if err := e.Pick("term"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Expand("   ", 10); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空查询错误: got %v", err)
	}
	if _, err := e.Expand("a b c d e f g h i", 10); !errors.Is(err, ErrQueryTooLong) {
		t.Fatalf("过长查询错误: got %v", err)
	}
	if _, err := e.Expand("term", 0); !errors.Is(err, ErrInvalidMaxExp) {
		t.Fatalf("非法上限错误: got %v", err)
	}

	got := mustExpand(t, e, "term", 10)
	assertCandidate(t, got.Candidates[0], Candidate{Term: "term", ConditionalDF: 1, Score: 3},
		"被拒绝 Expand 不递增 T；首次成功 Expand 为 t=1，点选有效")

	mustExpand(t, e, "missing", 10)
	got = mustExpand(t, e, "term", 10)
	assertCandidate(t, got.Candidates[0], Candidate{Term: "term", ConditionalDF: 1, Score: 3},
		"结果为空的成功 Expand 仍递增 T；此时 t-c=3 恰仍有效")

	got = mustExpand(t, e, "term", 10)
	assertCandidate(t, got.Candidates[0], Candidate{Term: "term", ConditionalDF: 1, Score: 1},
		"下一次 t-c=4，点选失效")
}

func TestPickSurvivesDeletionAndReappears(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "term")
	if err := e.Pick("term"); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove("d1"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("term"); !errors.Is(err, ErrTermNotFound) {
		t.Fatalf("词项不存在时 Pick: got %v", err)
	}

	mustIndex(t, e, "d1", "term")
	mustExpand(t, e, "missing", 10)
	got := mustExpand(t, e, "term", 10)
	assertCandidate(t, got.Candidates[0], Candidate{Term: "term", ConditionalDF: 1, Score: 3},
		"删除保留点选记录，重新出现且仍在窗口内时照常计分")
}

func TestMissingCompleteWordIsEmptySuccess(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "apple")
	got := mustExpand(t, e, "missing apple", 10)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{},
	}, "完整词不在词典中不是错误，条件文档集为空")
}

func TestCompleteQueryWithoutPrefixReturnsAllConditionalDocs(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d2", "anchor")
	mustIndex(t, e, "d1", "anchor")

	got := mustExpand(t, e, "anchor ", 10)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{"d1", "d2"},
	}, "没有前缀词时不产生候选，命中文档就是条件文档集")
}

func TestDuplicateCompleteWordsAndExactlyEightWords(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "a", "b")

	got := mustExpand(t, e, "a a a b ", 10)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{"d1"},
	}, "重复完整词不重复缩小文档集")

	got = mustExpand(t, e, "a a a a a a a a ", 10)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{},
		Hits:       []string{"d1"},
	}, "恰 8 个词允许")

	if _, err := e.Expand("a a a a a a a a p", 10); !errors.Is(err, ErrQueryTooLong) {
		t.Fatalf("9 个词（含前缀词）应拒绝: got %v", err)
	}
}

func TestSpecExample(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "apple", "apply", "ape")
	mustIndex(t, e, "d2", "apple", "apply")
	mustIndex(t, e, "d3", "apple", "apex")

	first := mustExpand(t, e, "ap", 2)
	assertResult(t, first, ExpandResult{
		Candidates: []Candidate{
			{Term: "apple", ConditionalDF: 3, Score: 3},
			{Term: "apply", ConditionalDF: 2, Score: 2},
		},
		Truncated: true,
		Hits:      []string{"d1", "d2", "d3"},
	}, "题面：第 1 次 Expand")

	if err := e.Pick("ape"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("ape"); err != nil {
		t.Fatal(err)
	}
	second := mustExpand(t, e, "ap", 2)
	assertResult(t, second, ExpandResult{
		Candidates: []Candidate{
			{Term: "ape", ConditionalDF: 1, Score: 3},
			{Term: "apple", ConditionalDF: 3, Score: 3},
		},
		Truncated: true,
		Hits:      []string{"d1", "d2", "d3"},
	}, "题面：第 2 次 Expand")

	if err := e.Pick("apex"); err != nil {
		t.Fatal(err)
	}
	third := mustExpand(t, e, "ap", 2)
	assertResult(t, third, ExpandResult{
		Candidates: []Candidate{
			{Term: "ape", ConditionalDF: 1, Score: 3},
			{Term: "apex", ConditionalDF: 1, Score: 3},
		},
		Truncated: true,
		Hits:      []string{"d1", "d3"},
	}, "题面：第 3 次 Expand")

	fourth := mustExpand(t, e, "ap", 2)
	assertResult(t, fourth, third, "题面：第 4 次 Expand 中 ape 仍有效")

	fifth := mustExpand(t, e, "ap", 2)
	assertResult(t, fifth, ExpandResult{
		Candidates: []Candidate{
			{Term: "apex", ConditionalDF: 1, Score: 3},
			{Term: "apple", ConditionalDF: 3, Score: 3},
		},
		Truncated: true,
		Hits:      []string{"d1", "d2", "d3"},
	}, "题面：第 5 次 Expand 中 ape 失效")
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	e := NewQueryExpander()
	mustIndex(t, e, "d1", "term")

	if err := e.Index("", []string{"term"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空 docID: got %v", err)
	}
	if err := e.Index("d2", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空 terms: got %v", err)
	}
	if err := e.Index("d2", []string{"", "term"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("含空词项: got %v", err)
	}
	if err := e.Index("d2", []string{"bad term"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("含空格词项: got %v", err)
	}
	if err := e.Index("d1", []string{"again"}); !errors.Is(err, ErrDuplicateDocID) {
		t.Fatalf("重复 docID: got %v", err)
	}
	if err := e.Remove("missing"); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("删除不存在文档: got %v", err)
	}
	if err := e.Pick(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空点选词项: got %v", err)
	}
	if err := e.Pick("bad term"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("含空格点选词项: got %v", err)
	}
	if err := e.Pick("missing"); !errors.Is(err, ErrTermNotFound) {
		t.Fatalf("点选不存在词项: got %v", err)
	}

	got := mustExpand(t, e, "term", 10)
	assertCandidate(t, got.Candidates[0], Candidate{Term: "term", ConditionalDF: 1, Score: 1},
		"所有拒绝操作均未新增点选或改变 T")
}

func TestRegistrationOrderIndependence(t *testing.T) {
	first := NewQueryExpander()
	second := NewQueryExpander()
	mustIndex(t, first, "d1", "z", "a")
	mustIndex(t, first, "d2", "a")
	mustIndex(t, second, "d2", "a")
	mustIndex(t, second, "d1", "a", "z")

	gotFirst := mustExpand(t, first, "a", 10)
	gotSecond := mustExpand(t, second, "a", 10)
	assertResult(t, gotFirst, gotSecond, "展开结果与文档登记顺序无关")
}
