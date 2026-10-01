package snippets

import (
	"bytes"
	"reflect"
	"sync"
	"testing"
)

func mustRegister(t *testing.T, r *Registry, id string, text string) {
	t.Helper()
	if err := r.Register(id, []byte(text)); err != nil {
		t.Fatalf("Register(%q) unexpected error: %v", id, err)
	}
}

func snippetsOf(t *testing.T, r *Registry, id string, hits []Hit, W, K int) []Snippet {
	t.Helper()
	got, err := r.Snippets(id, hits, W, K)
	if err != nil {
		t.Fatalf("Snippets(%q) unexpected error: %v", id, err)
	}
	return got
}

// 命中 e 恰等于 a+W 被包含；比 a+W 大 1 则放不进窗口，最大分值 0 时结束。
func TestExactWindowEdgeAndOneByteOver(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijkl") // len 12

	got := snippetsOf(t, r, "d", []Hit{{2, 10}}, 8, 1)
	want := Snippet{Start: 2, End: 10, Highlights: []Range{{2, 10}}, Score: 1}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("e==a+W should be included, got %v", got)
	}

	got = snippetsOf(t, r, "d", []Hit{{2, 11}}, 8, 1)
	if len(got) != 0 {
		t.Fatalf("e==a+W+1 cannot fit; expect empty, got %v", got)
	}
}

// 分值并列时取起点最小者。
func TestTiePicksSmallestStart(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnopqrst")
	got := snippetsOf(t, r, "d", []Hit{{5, 7}, {0, 2}}, 3, 2)
	if len(got) != 2 ||
		got[0].Start != 0 || got[1].Start != 5 ||
		got[0].Score != 1 || got[1].Score != 1 {
		t.Fatalf("tie should pick a=0 first, got %v", got)
	}
}

// 重叠命中各自计分（去重后），高亮区间合并。
func TestOverlappingHitsScoreAndMerge(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got := snippetsOf(t, r, "d", []Hit{{2, 5}, {1, 4}, {1, 4}, {3, 6}}, 5, 1)
	if len(got) != 1 {
		t.Fatalf("expect 1 snippet, got %d", len(got))
	}
	s := got[0]
	if s.Start != 1 || s.End != 6 || s.Score != 3 {
		t.Fatalf("expect [1,6) score 3 (dedup), got %+v", s)
	}
	if len(s.Highlights) != 1 || s.Highlights[0] != (Range{1, 6}) {
		t.Fatalf("expect merged highlight [1,6), got %v", s.Highlights)
	}
}

// 相接命中 [1,3) 与 [3,5) 的高亮不合并。
func TestAdjacentHighlightsNotMerged(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got := snippetsOf(t, r, "d", []Hit{{1, 3}, {3, 5}}, 5, 1)
	if len(got) != 1 || got[0].Score != 2 {
		t.Fatalf("expect 1 snippet score 2, got %v", got)
	}
	hl := got[0].Highlights
	if len(hl) != 2 || hl[0] != (Range{1, 3}) || hl[1] != (Range{3, 5}) {
		t.Fatalf("adjacent ranges must not merge, got %v", hl)
	}
}

// 命中比 W 更长而被忽略，且所有候选分值均为 0 时结束。
func TestHitLongerThanWindowScoreZeroEnds(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got := snippetsOf(t, r, "d", []Hit{{2, 8}, {3, 9}}, 3, 4)
	if len(got) != 0 {
		t.Fatalf("no hit fits W=3; expect empty, got %v", got)
	}
}

// 窗口右端被后面的已选片段起点截短；跨边界命中因与已选片段相交而不再可用；
// 片段按 Start 升序返回，与选取顺序（右侧高分片段先选）不同。
func TestWindowClampedAndIntersectionAndOrder(t *testing.T) {
	r := NewRegistry()
	// 10 个 3 字节汉字，共 30 字节，天然提供字符边界。
	mustRegister(t, r, "d", "あいうえおかきくけこ")

	// 文本边界为 3 的倍数：0,3,...,30。
	// 第一轮 a=18 以 4 分胜出，选中 [18,30)；[21,24) 虽与该片段相交，
	// 但已计入 C(18)。第二轮 [21,24) 与已选片段相交而不再可用，
	// a=0 的窗口右端被已选片段起点 18 截短（a+W=22），
	// 左侧 [0,3) 第二轮才选中，但按 Start 升序排在输出最前。
	hits := []Hit{{0, 3}, {18, 27}, {21, 24}, {24, 27}, {27, 30}}
	got := snippetsOf(t, r, "d", hits, 22, 16)

	if len(got) != 2 {
		t.Fatalf("expect 2 snippets, got %d: %v", len(got), got)
	}
	left, right := got[0], got[1]
	if left.Start != 0 || left.End != 3 || left.Score != 1 {
		t.Fatalf("left snippet expect [0,3) score 1, got %+v", left)
	}
	if len(left.Highlights) != 1 || left.Highlights[0] != (Range{0, 3}) {
		t.Fatalf("left highlights expect [0,3), got %v", left.Highlights)
	}
	if right.Start != 18 || right.End != 30 || right.Score != 4 {
		t.Fatalf("right snippet expect [18,30) score 4, got %+v", right)
	}
	// [18,27) 与 [27,30) 相接不合并。
	if len(right.Highlights) != 2 ||
		right.Highlights[0] != (Range{18, 27}) || right.Highlights[1] != (Range{27, 30}) {
		t.Fatalf("right highlights expect [18,27),[27,30), got %v", right.Highlights)
	}
}

// 端点落在多字节字符内部时，整次调用被拒绝。
func TestEndpointInsideMultibyteCharRejected(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "你好世界") // 边界: 0,3,6,9,12

	cases := [][]Hit{
		{{0, 2}},          // e 在字符内部
		{{1, 3}},          // s 在字符内部
		{{0, 12}, {3, 4}}, // 混入内部端点，整次拒绝
	}
	for i, hs := range cases {
		if _, err := r.Snippets("d", hs, 5, 1); err != ErrInvalidArgument {
			t.Fatalf("case %d: expect ErrInvalidArgument, got %v", i, err)
		}
	}
	got := snippetsOf(t, r, "d", []Hit{{0, 3}, {3, 9}}, 9, 2)
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 9 || got[0].Score != 2 {
		t.Fatalf("boundary-aligned hits should work, got %v", got)
	}
}

// K 大于可选片段数时提前结束。
func TestKLargerThanSelectable(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got := snippetsOf(t, r, "d", []Hit{{0, 2}}, 3, 16)
	if len(got) != 1 {
		t.Fatalf("expect 1 snippet, got %d", len(got))
	}
}

// hits 为空合法，返回非 nil 空切片。
func TestEmptyHits(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abc")
	got, err := r.Snippets("d", nil, 3, 1)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty hits expect non-nil empty slice, got %v, %v", got, err)
	}
}

// 拒绝原因可区分，且被拒绝的操作不改变登记表。
func TestRejectionReasonsAndStateUnchanged(t *testing.T) {
	r := NewRegistry()

	if err := r.Register("", []byte("x")); err != ErrInvalidArgument {
		t.Fatalf("empty docID: %v", err)
	}
	if err := r.Register("d", []byte{0xff}); err != ErrInvalidArgument {
		t.Fatalf("invalid utf8: %v", err)
	}
	big := bytes.Repeat([]byte("a"), MaxTextLen+1)
	if err := r.Register("d", big); err != ErrInvalidArgument {
		t.Fatalf("too long: %v", err)
	}

	mustRegister(t, r, "d", "abc")
	if err := r.Register("d", []byte("zz")); err != ErrDuplicateDocument {
		t.Fatalf("duplicate: %v", err)
	}
	if err := r.Unregister("missing"); err != ErrDocumentNotFound {
		t.Fatalf("unregister missing: %v", err)
	}
	if _, err := r.Snippets("missing", nil, 1, 1); err != ErrDocumentNotFound {
		t.Fatalf("snippets missing doc: %v", err)
	}

	if _, err := r.Snippets("d", nil, 0, 1); err != ErrInvalidArgument {
		t.Fatalf("W out of range: %v", err)
	}
	if _, err := r.Snippets("d", nil, 4097, 1); err != ErrInvalidArgument {
		t.Fatalf("W too large: %v", err)
	}
	if _, err := r.Snippets("d", nil, 1, 0); err != ErrInvalidArgument {
		t.Fatalf("K out of range: %v", err)
	}
	if _, err := r.Snippets("d", nil, 1, 17); err != ErrInvalidArgument {
		t.Fatalf("K too large: %v", err)
	}
	if _, err := r.Snippets("d", []Hit{{0, 4}}, 3, 1); err != ErrInvalidArgument {
		t.Fatalf("hit beyond text: %v", err)
	}
	if _, err := r.Snippets("d", []Hit{{2, 2}}, 3, 1); err != ErrInvalidArgument {
		t.Fatalf("s==e: %v", err)
	}

	// 非法操作之后原文档仍可正常使用，重复登记未覆盖原文。
	got := snippetsOf(t, r, "d", []Hit{{0, 2}}, 2, 1)
	if len(got) != 1 || got[0].End != 2 {
		t.Fatalf("state should be unchanged, got %v", got)
	}
	if err := r.Unregister("d"); err != nil {
		t.Fatalf("original doc should still exist: %v", err)
	}
}

// hits 去重前超过 10000 个即拒绝；恰好 10000 个（去重后 1 个）合法。
func TestTooManyHitsRejected(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	many := make([]Hit, MaxHits+1)
	for i := range many {
		many[i] = Hit{0, 1}
	}
	if _, err := r.Snippets("d", many, 3, 1); err != ErrInvalidArgument {
		t.Fatalf("expect rejection for %d hits pre-dedup", len(many))
	}
	ok := make([]Hit, MaxHits)
	for i := range ok {
		ok[i] = Hit{0, 1}
	}
	if _, err := r.Snippets("d", ok, 3, 1); err != nil {
		t.Fatalf("10000 duplicates should be accepted: %v", err)
	}
}

// Register 保存副本；返回结果不别名内部状态。
func TestNoAliasing(t *testing.T) {
	r := NewRegistry()
	text := []byte("abcdefghij")
	if err := r.Register("d", text); err != nil {
		t.Fatal(err)
	}
	text[0] = 'Z'
	got := snippetsOf(t, r, "d", []Hit{{0, 2}}, 2, 1)
	if len(got) != 1 || got[0].Start != 0 {
		t.Fatalf("mutating caller text must not affect registry, got %v", got)
	}
	got[0].Start = 999
	got[0].Highlights[0] = Range{9, 9}
	again := snippetsOf(t, r, "d", []Hit{{0, 2}}, 2, 1)
	if again[0].Start != 0 || again[0].Highlights[0] != (Range{0, 2}) {
		t.Fatalf("mutating returned snippets must not affect registry, got %v", again)
	}
}

// 同组命中的乱序与重复不影响结果。
func TestOrderIndependentWithDuplicates(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmn")
	a := []Hit{{9, 11}, {0, 3}, {1, 4}, {0, 3}, {8, 12}, {1, 4}}
	b := []Hit{{0, 3}, {1, 4}, {8, 12}, {9, 11}, {1, 4}, {0, 3}}
	ga := snippetsOf(t, r, "d", a, 5, 4)
	gb := snippetsOf(t, r, "d", b, 5, 4)
	if !reflect.DeepEqual(ga, gb) {
		t.Fatalf("results must be order-independent: %v vs %v", ga, gb)
	}
}

// 并发调用 Register/Unregister/Snippets，配合 -race 验证。
func TestConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("d", []byte("abcdefghij")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := "d"
				if i%4 == 0 {
					id = "tmp"
					_ = r.Register(id, []byte("abc"))
				}
				_, _ = r.Snippets("d", []Hit{{0, 2}}, 2, 1)
				_ = r.Unregister(id)
				_ = r.Register(id, []byte("abcdefghij"))
			}
		}(i)
	}
	wg.Wait()
}
