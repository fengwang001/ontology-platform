package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func ids(v ...string) []ObjectID {
	out := make([]ObjectID, len(v))
	for i, s := range v {
		out[i] = ObjectID(s)
	}
	return out
}

func equalIDs(a, b []ObjectID) bool {
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

// traversalLog 记录每次遍历请求的输入、输出与截断原因，供审计与随机对照留存。
var traversalLog requestLog

type requestLog struct {
	mu    sync.Mutex
	entry []logEntry
}

type logEntry struct {
	Start      ObjectID   `json:"start"`
	MaxDepth   int        `json:"max_depth"`
	MaxFanout  int        `json:"max_fanout"`
	PageSize   int        `json:"page_size"`
	TokenUsed  string     `json:"token_used,omitempty"`
	Output     []ObjectID `json:"output"`
	Truncation string     `json:"truncation"`
	NextToken  string     `json:"next_token,omitempty"`
}

func (l *requestLog) reset() {
	l.mu.Lock()
	l.entry = nil
	l.mu.Unlock()
}

func (l *requestLog) record(params TraverseParams, out []ObjectID, reason TruncationReason, nextTokens ...string) {
	e := logEntry{
		Start:      params.Start,
		MaxDepth:   params.MaxDepth,
		MaxFanout:  params.MaxFanout,
		PageSize:   params.PageSize,
		TokenUsed:  params.Token,
		Output:     append([]ObjectID(nil), out...),
		Truncation: reason.String(),
	}
	if len(nextTokens) > 0 {
		e.NextToken = nextTokens[0]
	}
	l.mu.Lock()
	l.entry = append(l.entry, e)
	l.mu.Unlock()
}

func drain(t *testing.T, tr *Traverser, caller *Principal, p TraverseParams) ([]ObjectID, TruncationReason) {
	t.Helper()
	var all []ObjectID
	var reason TruncationReason
	for {
		page, err := tr.Traverse(caller, p)
		if err != nil {
			t.Fatalf("traverse: %v", err)
		}
		all = append(all, page.Objects...)
		reason = page.Truncation
		traversalLog.record(p, page.Objects, reason, page.NextToken)
		if page.NextToken == "" {
			break
		}
		p.Token = page.NextToken
	}
	return all, reason
}

// TestFanoutPriorityOverDepth：扇出截断与深度截断同时成立时只报扇出。
func TestFanoutPriorityOverDepth(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b", "c", "d"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "b"})
	st.AddLink(Link{Type: "edge", From: "s", To: "c"})
	st.AddLink(Link{Type: "edge", From: "a", To: "d"})

	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 2, PageSize: 1})
	if reason != TruncationFanout {
		t.Fatalf("want fanout truncation, got %v", reason)
	}
	if want := ids("s", "a", "b"); !equalIDs(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestDepthTruncation：仅深度截断。
func TestDepthTruncation(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	st.AddLink(Link{Type: "edge", From: "a", To: "b"})

	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 1})
	if reason != TruncationDepth {
		t.Fatalf("want depth truncation, got %v", reason)
	}
	if want := ids("s", "a"); !equalIDs(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestComplete：无任何截断。
func TestComplete(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 5, MaxFanout: 10, PageSize: 1})
	if reason != TruncationNone {
		t.Fatalf("want complete, got %v", reason)
	}
	if want := ids("s", "a"); !equalIDs(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestDegenerateDepthZero：深度为零只返回起点；起点本有后继则为深度截断。
func TestDegenerateDepthZero(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	st.AddObject(Object{ID: "s"})
	st.AddObject(Object{ID: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	tr := NewTraverser(st)

	page, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 0, MaxFanout: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(page.Objects, ids("s")) || page.Truncation != TruncationDepth {
		t.Fatalf("got %v reason %v", page.Objects, page.Truncation)
	}

	isolated := NewStore(AllowAllPolicy{})
	isolated.AddObject(Object{ID: "z"})
	page, err = NewTraverser(isolated).Traverse(nil, TraverseParams{Start: "z", MaxDepth: 0, MaxFanout: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(page.Objects, ids("z")) || page.Truncation != TruncationNone {
		t.Fatalf("isolated depth-0: got %v reason %v", page.Objects, page.Truncation)
	}
}

// TestDegenerateFanoutZero：扇出为零时不保留任何后继，且报扇出截断（优先于深度）。
func TestDegenerateFanoutZero(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLink(Link{Type: "edge", From: "s", To: "a"})
	st.AddLink(Link{Type: "edge", From: "s", To: "b"})
	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 3, MaxFanout: 0, PageSize: 1})
	if !equalIDs(got, ids("s")) {
		t.Fatalf("got %v", got)
	}
	if reason != TruncationFanout {
		t.Fatalf("reason = %v want fanout", reason)
	}
}

// TestPermissionDoesNotConsumeFanout：不可见对象/不可遍历链接不占扇出名额。
func TestPermissionDoesNotConsumeFanout(t *testing.T) {
	alice := &Principal{Name: "alice"}
	policy := NewACLPolicy(
		map[*Principal][]ObjectID{alice: {"hidden"}},
		map[*Principal][]Link{
			alice: {{Type: "edge", From: "s", To: "blocked"}},
		},
	)
	st := NewStore(policy)
	for _, id := range []string{"s", "hidden", "blocked", "x", "y"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	// 邻接按目标排序后 blocked, hidden, x, y；前两者无效，x/y 必须占满 2 个名额。
	st.AddLink(Link{Type: "edge", From: "s", To: "x"})
	st.AddLink(Link{Type: "edge", From: "s", To: "y"})
	st.AddLink(Link{Type: "edge", From: "s", To: "blocked"})
	st.AddLink(Link{Type: "edge", From: "s", To: "hidden"})

	tr := NewTraverser(st)
	got, reason := drain(t, tr, alice, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 2, PageSize: 10})
	if !equalIDs(got, ids("s", "x", "y")) {
		t.Fatalf("got %v", got)
	}
	if reason != TruncationNone {
		t.Fatalf("invisible objects consumed fanout: reason %v", reason)
	}
}

// TestStartForbidden：起点无存在性权限 -> ErrForbidden。
func TestStartForbidden(t *testing.T) {
	alice := &Principal{Name: "alice"}
	policy := NewACLPolicy(map[*Principal][]ObjectID{alice: {"s"}}, nil)
	st := NewStore(policy)
	st.AddObject(Object{ID: "s"})
	_, err := NewTraverser(st).Traverse(alice, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 1})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v want ErrForbidden", err)
	}
}

// TestInvalidArguments：负深度/负扇出/空起点/坏标记。
func TestInvalidArguments(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	st.AddObject(Object{ID: "s"})
	tr := NewTraverser(st)
	if _, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: -1, MaxFanout: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg depth: %v", err)
	}
	if _, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: -1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg fanout: %v", err)
	}
	if _, err := tr.Traverse(nil, TraverseParams{MaxDepth: 1, MaxFanout: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty start: %v", err)
	}
	if _, err := tr.Traverse(nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 1, Token: "not-a-token"}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("bad token: %v", err)
	}
	if _, err := tr.Traverse(nil, TraverseParams{MaxDepth: 1, MaxFanout: 1, Token: "x.y"}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("forged token: %v", err)
	}
}

var _ = fmt.Sprintf
