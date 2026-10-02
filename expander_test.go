package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustIndex(t *testing.T, e *Expander, docID string, terms ...string) {
	t.Helper()
	if err := e.Index(docID, terms); err != nil {
		t.Fatalf("Index(%q,%v): %v", docID, terms, err)
	}
}

func cand(term string, df, score int) Candidate {
	return Candidate{Term: term, CondDF: df, Score: score}
}

func expandOK(t *testing.T, e *Expander, query string, maxExp int) ExpandResult {
	t.Helper()
	r, err := e.Expand(query, maxExp)
	if err != nil {
		t.Fatalf("Expand(%q,%d): %v", query, maxExp, err)
	}
	return r
}

func assertResult(t *testing.T, got, want ExpandResult, ctx string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %+v\n want %+v", ctx, got, want)
	}
}

// 末尾有空格 vs 无空格：同一末词分别作完整词与前缀词；前缀词恰等于词项。
func TestTrailingSpaceFullVsPrefix(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "apple", "apples")

	got := expandOK(t, e, "apple", 100)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{cand("apple", 1, 1), cand("apples", 1, 1)},
		Hits:       []string{"d1"},
	}, "prefix")

	got = expandOK(t, e, "apple ", 100)
	assertResult(t, got, ExpandResult{Candidates: []Candidate{}, Hits: []string{"d1"}}, "full")

	got = expandOK(t, e, "  apple   ", 100)
	assertResult(t, got, ExpandResult{Candidates: []Candidate{}, Hits: []string{"d1"}}, "spaces")
}

// maxExp 恰等于候选数时不截断，少 1 时截断。
func TestTruncationBoundary(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "a1")
	mustIndex(t, e, "d2", "a2")
	mustIndex(t, e, "d3", "a3")

	r := expandOK(t, e, "a", 3)
	if r.Truncated || len(r.Candidates) != 3 {
		t.Fatalf("equal: %+v", r)
	}
	r = expandOK(t, e, "a", 2)
	if !r.Truncated || len(r.Candidates) != 2 {
		t.Fatalf("one less: %+v", r)
	}
}

var _ = errors.Is

// 题面给出的完整例子（含 t-c 恰为 3 仍有效、为 4 失效）。
func TestSpecExample(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "apple", "apply", "ape")
	mustIndex(t, e, "d2", "apple", "apply")
	mustIndex(t, e, "d3", "apple", "apex")

	r1 := expandOK(t, e, "ap", 2)
	assertResult(t, r1, ExpandResult{
		Candidates: []Candidate{cand("apple", 3, 3), cand("apply", 2, 2)},
		Truncated:  true,
		Hits:       []string{"d1", "d2", "d3"},
	}, "expand 1")

	if err := e.Pick("ape"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("ape"); err != nil { // 同一 c 重复 Pick 幂等
		t.Fatal(err)
	}

	r2 := expandOK(t, e, "ap", 2)
	assertResult(t, r2, ExpandResult{
		Candidates: []Candidate{cand("ape", 1, 3), cand("apple", 3, 3)},
		Truncated:  true,
		Hits:       []string{"d1", "d2", "d3"},
	}, "expand 2")

	if err := e.Pick("apex"); err != nil {
		t.Fatal(err)
	}

	r3 := expandOK(t, e, "ap", 2)
	assertResult(t, r3, ExpandResult{
		Candidates: []Candidate{cand("ape", 1, 3), cand("apex", 1, 3)},
		Truncated:  true,
		Hits:       []string{"d1", "d3"},
	}, "expand 3")

	r4 := expandOK(t, e, "ap", 2) // t-c: 4-1=3，ape 仍有效
	assertResult(t, r4, ExpandResult{
		Candidates: []Candidate{cand("ape", 1, 3), cand("apex", 1, 3)},
		Truncated:  true,
		Hits:       []string{"d1", "d3"},
	}, "expand 4 boundary active")

	r5 := expandOK(t, e, "ap", 2) // t-c=4，ape 失效
	assertResult(t, r5, ExpandResult{
		Candidates: []Candidate{cand("apex", 1, 3), cand("apple", 3, 3)},
		Truncated:  true,
		Hits:       []string{"d1", "d2", "d3"},
	}, "expand 5 expired")
}

// 同一 c 重复 Pick 只计一次；不同 c 各计一次。
func TestPickDedupAndDistinctC(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "ape")

	for i := 0; i < 3; i++ {
		if err := e.Pick("ape"); err != nil {
			t.Fatal(err)
		}
	}
	r := expandOK(t, e, "ape", 10)
	if r.Candidates[0].Score != 3 {
		t.Fatalf("same-c dedup score: %+v", r.Candidates)
	}

	if err := e.Pick("ape"); err != nil { // 此刻 T=1，c 不同
		t.Fatal(err)
	}
	r = expandOK(t, e, "ape", 10)
	if r.Candidates[0].Score != 5 { // 1 + 2*(1+1)
		t.Fatalf("distinct-c score: %+v", r.Candidates)
	}
}

// 点选词项删除后重新出现，记录保留并照常计分；同 docID 重新登记视为新文档。
func TestPickSurvivesRemoval(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "ape")
	if err := e.Pick("ape"); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove("d1"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("ape"); !errors.Is(err, ErrTermNotFound) {
		t.Fatalf("pick absent term: %v", err)
	}
	r := expandOK(t, e, "ape", 10)
	if len(r.Candidates) != 0 || len(r.Hits) != 0 {
		t.Fatalf("empty corpus result: %+v", r)
	}
	mustIndex(t, e, "d1", "ape")
	r = expandOK(t, e, "ape", 10)
	if r.Candidates[0].Score != 3 {
		t.Fatalf("click survives deletion: %+v", r.Candidates)
	}
}

// 被拒绝的 Expand 不推进 T；结果为空的成功 Expand 推进 T。
func TestRejectedVsEmptyExpandT(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "ape")
	if err := e.Pick("ape"); err != nil {
		t.Fatal(err)
	}

	bad := []struct {
		q      string
		maxExp int
		want   error
	}{
		{"", 10, ErrEmptyQuery},
		{"   ", 10, ErrEmptyQuery},
		{"a b c d e f g h i", 10, ErrQueryTooLong},
		{"a", 0, ErrInvalidMaxExp},
		{"a", 1001, ErrInvalidMaxExp},
	}
	for _, b := range bad {
		if _, err := e.Expand(b.q, b.maxExp); !errors.Is(err, b.want) {
			t.Fatalf("Expand(%q,%d)=%v want %v", b.q, b.maxExp, err, b.want)
		}
	}

	r := expandOK(t, e, "ape", 10) // T: 0 -> 1，点选仍有效
	if r.Candidates[0].Score != 3 {
		t.Fatalf("rejected expands must not advance T: %+v", r.Candidates)
	}

	for i := 0; i < 3; i++ {
		empty := expandOK(t, e, "zzz", 10) // 无候选但成功，推进 T
		if len(empty.Candidates) != 0 {
			t.Fatalf("want no candidates: %+v", empty)
		}
	}
	r = expandOK(t, e, "ape", 10) // T=5，t-c=4，点选失效
	if r.Candidates[0].Score != 1 {
		t.Fatalf("empty expands must advance T: %+v", r.Candidates)
	}
}

// 完整词不在词典时结果为空而非错误；重复完整词计入词数；恰 8 个词合法。
func TestUnknownCompleteAndLength(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "apple", "ape")

	r, err := e.Expand("nope ap", 10)
	if err != nil || len(r.Candidates) != 0 || r.Truncated || len(r.Hits) != 0 {
		t.Fatalf("unknown complete word: %+v err=%v", r, err)
	}

	r = expandOK(t, e, "apple apple ap", 10)
	if len(r.Candidates) != 2 {
		t.Fatalf("duplicate complete terms still count: %+v", r)
	}

	r = expandOK(t, e, "apple apple apple apple apple apple apple ape", 10)
	if len(r.Candidates) != 1 || r.Candidates[0].Term != "ape" {
		t.Fatalf("exactly 8 terms must be legal: %+v", r)
	}

	// 查询内重复词去重后用于条件集（两篇都含 apple 不影响）。
	mustIndex(t, e, "d2", "apple")
	r = expandOK(t, e, "apple apple ", 10)
	assertResult(t, r, ExpandResult{Candidates: []Candidate{}, Hits: []string{"d1", "d2"}}, "dup full terms trailing space")
}

// 拒绝原因与顺序、被拒绝操作不改状态。
func TestRejectionsDoNotMutateState(t *testing.T) {
	e := New()
	if err := e.Index("", []string{"a"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty docID: %v", err)
	}
	if err := e.Index("d", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty terms: %v", err)
	}
	if err := e.Index("d", []string{""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty term: %v", err)
	}
	if err := e.Index("d", []string{"a b"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("space in term: %v", err)
	}
	mustIndex(t, e, "d", "a")
	if err := e.Index("d", []string{"b"}); !errors.Is(err, ErrDuplicateDoc) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := e.Remove("nope"); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if err := e.Pick(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("pick empty: %v", err)
	}
	if err := e.Pick("a b"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("pick space: %v", err)
	}
	if err := e.Pick("ghost"); !errors.Is(err, ErrTermNotFound) {
		t.Fatalf("pick missing term: %v", err)
	}

	// 全部拒绝后状态不变：一次 Expand 即 T=1，无点选加成。
	r := expandOK(t, e, "a", 10)
	if r.Candidates[0].Score != 1 {
		t.Fatalf("state mutated by rejected ops: %+v", r.Candidates)
	}
}

// 返回切片不别名内部状态。
func TestNoAliasing(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "abc", "abd")
	r := expandOK(t, e, "ab", 10)
	r.Candidates[0] = cand("HACK", 99, 99)
	r.Hits[0] = "HACK"

	r2 := expandOK(t, e, "ab", 10)
	assertResult(t, r2, ExpandResult{
		Candidates: []Candidate{cand("abc", 1, 1), cand("abd", 1, 1)},
		Hits:       []string{"d1"},
	}, "internal state must not alias returned slices")

	terms := []string{"x"}
	mustIndex(t, e, "d2", terms...)
	terms[0] = "HACK"
	r3 := expandOK(t, e, "x", 10)
	if len(r3.Candidates) != 1 || r3.Candidates[0].Term != "x" {
		t.Fatalf("input slice aliased: %+v", r3.Candidates)
	}
}

// 条件 df 与全局 df 不同；条件 df 为 0 的词项不占名额、不影响 Truncated。
func TestConditionalDF(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "zebra", "cat")
	mustIndex(t, e, "d2", "zebra")
	mustIndex(t, e, "d3", "zebra")
	mustIndex(t, e, "d4", "zoo", "cat")

	got := expandOK(t, e, "cat z", 10)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{cand("zebra", 1, 1), cand("zoo", 1, 1)},
		Hits:       []string{"d1", "d4"},
	}, "cond df differs from global df")

	got = expandOK(t, e, "zoo z", 1)
	assertResult(t, got, ExpandResult{
		Candidates: []Candidate{cand("zoo", 1, 1)},
		Hits:       []string{"d4"},
	}, "zero cond df excluded without truncation")
}

// 得分并列按词项字节序（区分大小写，大写字节序在前）。
func TestTieBreakByteOrder(t *testing.T) {
	e := New()
	mustIndex(t, e, "d1", "tz", "ta", "tM")
	want := ExpandResult{
		Candidates: []Candidate{cand("tM", 1, 1), cand("ta", 1, 1), cand("tz", 1, 1)},
		Hits:       []string{"d1"},
	}
	got := expandOK(t, e, "t", 10)
	assertResult(t, got, want, "tie byte order")
}
