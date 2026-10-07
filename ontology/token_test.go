package ontology

import (
	"errors"
	"testing"
)

// TestTokenReplayAndSnapshotStability：续读标记锚定生成时快照，
// 之后新增/删除均不影响续读；同一标记重复使用返回完全相同的下一页。
func TestTokenReplayAndSnapshotStability(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b", "c", "d"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "b"})
	st.AddLink(Link{Type: "edge", From: "s", To: "c"})

	tr := NewTraverser(st)
	first, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(first.Objects, ids("s", "a")) {
		t.Fatalf("first page: %v", first.Objects)
	}

	st.AddLink(Link{Type: "edge", From: "a", To: "d"})
	st.DeleteObject("b")

	second1, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken})
	if err != nil {
		t.Fatalf("resume after mutation: %v", err)
	}
	second2, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !equalIDs(second1.Objects, second2.Objects) || !equalIDs(second1.Objects, ids("b", "c")) {
		t.Fatalf("snapshot drift: %v vs %v", second1.Objects, second2.Objects)
	}
	if second1.NextToken != second2.NextToken {
		t.Fatalf("next token drifted: %q vs %q", second1.NextToken, second2.NextToken)
	}
	if second1.NextToken == "" {
		return // 末页：序列恰好被页边界切完，重放同一标记仍返回相同末页。
	}

	final, err := tr.Traverse(nil, TraverseParams{Token: second1.NextToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Objects) != 0 || final.NextToken != "" {
		t.Fatalf("final page not exhausted: %v", final.Objects)
	}
}

// TestStartDeletedDuringPagination：起点在续读期间于当前状态被删除，
// 但锚定快照中仍存在，续读照常完成。
func TestStartDeletedDuringPagination(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "b"})
	tr := NewTraverser(st)

	first, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	st.DeleteObject("s")

	all := []ObjectID{}
	all = append(all, first.Objects...)
	token := first.NextToken
	for token != "" {
		page, err := tr.Traverse(nil, TraverseParams{Token: token, PageSize: 10})
		if err != nil {
			t.Fatalf("resume after start deletion: %v", err)
		}
		all = append(all, page.Objects...)
		token = page.NextToken
	}
	if !equalIDs(all, ids("s", "a", "b")) {
		t.Fatalf("got %v", all)
	}
}

// TestTokenObsoleteAfterHistoryPrune：锚定快照被释放后返回 ErrTokenObsolete。
func TestTokenObsoleteAfterHistoryPrune(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	st.AddObject(Object{ID: "s"})
	st.AddObject(Object{ID: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	tr := NewTraverser(st)
	first, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 制造新版本并释放全部历史。
	st.AddObject(Object{ID: "z"})
	st.PruneHistory(st.Current().Epoch())
	if _, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken}); !errors.Is(err, ErrTokenObsolete) {
		t.Fatalf("got %v want ErrTokenObsolete", err)
	}
}

// TestTokenKeyIsolation：跨遍历器（不同 HMAC 密钥）的标记互不可用。
func TestTokenKeyIsolation(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	st.AddObject(Object{ID: "s"})
	st.AddObject(Object{ID: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	first, err := NewTraverser(st).Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTraverser(st).Traverse(nil, TraverseParams{Token: first.NextToken}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("foreign token accepted: %v", err)
	}
}

// TestResumeWithMismatchedParams：续读参数与首次不一致按非法处理。
func TestResumeWithMismatchedParams(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	st.AddObject(Object{ID: "s"})
	st.AddObject(Object{ID: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	tr := NewTraverser(st)
	first, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 2, MaxFanout: 10, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken, MaxDepth: 3, MaxFanout: 10}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("depth mismatch: %v", err)
	}
	// 参数留 0 表示沿用标记内参数，必须成功。
	if _, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken}); err != nil {
		t.Fatalf("resume with implicit params failed: %v", err)
	}
}
