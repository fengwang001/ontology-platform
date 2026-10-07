package ontology

import (
	"reflect"
	"testing"
)

// 构造图：a -L-> b -L-> c（类型 T），另有一条 a -L-> d。
func buildChainStore(t *testing.T) (*Store, map[string]Version) {
	t.Helper()
	must := mkMust(t)
	s := NewStore()
	vs := map[string]Version{}
	vs["defT"] = must(s.DefineObjectType("T", []PropertyDef{
		{ID: "p", Name: "name", Type: TypeString},
	}))
	for _, id := range []string{"a", "b", "c", "d"} {
		vs["put"+id] = must(s.PutObject("T", id, map[string]Value{"name": id}))
	}
	vs["defL"] = must(s.DefineLinkType("L", "T", "T", ManyToMany))
	vs["ab"] = must(s.AddLink("L", "a", "b"))
	vs["bc"] = must(s.AddLink("L", "b", "c"))
	vs["ad"] = must(s.AddLink("L", "a", "d"))
	return s, vs
}

func errKindOf(err error) ErrorKind {
	if te, ok := err.(*TraverseError); ok {
		return te.Kind
	}
	return -1
}

func TestErrorPriorityStartNotExistBeatsLimits(t *testing.T) {
	s, vs := buildChainStore(t)
	// 起始对象不存在 + 访问上限为 0：应报告起始对象不存在。
	_, err := s.Traverse(TraverseRequest{Start: "ghost", AsOf: vs["ad"], MaxDepth: 0, MaxVisited: 0})
	if errKindOf(err) != ErrKindStartNotExist {
		t.Fatalf("got %v, want START_OBJECT_NOT_EXIST", err)
	}
}

func TestErrorPriorityBeforeHorizonBeatsLimits(t *testing.T) {
	s, vs := buildChainStore(t)
	s.Compact(vs["ad"])
	// asOf 早于边界 + 上限为 0：应报告早于边界。
	_, err := s.Traverse(TraverseRequest{Start: "a", AsOf: vs["defT"], MaxDepth: 0, MaxVisited: 0})
	if errKindOf(err) != ErrKindBeforeHorizon {
		t.Fatalf("got %v, want BEFORE_REPLAY_HORIZON", err)
	}
}

func TestErrorPriorityLimitBeatsMissing(t *testing.T) {
	s, vs := buildChainStore(t)
	asOf := vs["ad"]
	// 令链接 L/a/b 的历史在 asOf 缺失；同时 MaxVisited=1 使访问 b/c/d 必超限。
	s.DeclareGap("link", "L/a/b", vs["ab"], Open)
	_, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 1})
	if errKindOf(err) != ErrKindLimitExceeded {
		t.Fatalf("got %v, want TRAVERSAL_LIMIT_EXCEEDED", err)
	}
}

func TestErrorHistoryMissing(t *testing.T) {
	s, vs := buildChainStore(t)
	asOf := vs["ad"]
	s.DeclareGap("link", "L/a/b", vs["ab"], Open)
	_, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 16})
	if errKindOf(err) != ErrKindHistoryMissing {
		t.Fatalf("got %v, want HISTORY_MISSING", err)
	}
	// 对象属性历史缺失。
	s2, vs2 := buildChainStore(t)
	s2.DeclareGap("object-props", "b", vs2["ab"], Open)
	_, err = s2.Traverse(TraverseRequest{Start: "a", AsOf: vs2["ad"], MaxDepth: 8, MaxVisited: 16})
	if errKindOf(err) != ErrKindHistoryMissing {
		t.Fatalf("got %v, want HISTORY_MISSING", err)
	}
}

func TestErrorDepthAndVisitedLimits(t *testing.T) {
	s, vs := buildChainStore(t)
	asOf := vs["ad"]
	// 深度上限 1：b 在深度 1 可访问，但 b 的展开会发现深度 2 的 c。
	_, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 1, MaxVisited: 16})
	if errKindOf(err) != ErrKindLimitExceeded {
		t.Fatalf("depth: got %v, want TRAVERSAL_LIMIT_EXCEEDED", err)
	}
	// 访问规模上限 2：a,b 之后访问 c/d 超限。
	_, err = s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 2})
	if errKindOf(err) != ErrKindLimitExceeded {
		t.Fatalf("visited: got %v, want TRAVERSAL_LIMIT_EXCEEDED", err)
	}
	// 足够的上限：成功。
	res, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 4 || len(res.Edges) != 3 {
		t.Fatalf("got %d nodes %d edges, want 4/3", len(res.Nodes), len(res.Edges))
	}
}

// 任一错误都不得对历史轨迹产生可观察改动，也不得返回部分结果。
func TestErrorHasNoSideEffectAndNoPartialResult(t *testing.T) {
	s, vs := buildChainStore(t)
	asOf := vs["ad"]
	if _, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 16}); err != nil {
		t.Fatal(err)
	}
	vBefore := s.CurrentVersion()
	eventsBefore := len(s.Events())

	// 声明前先在更早的历史时刻取一次基准结果。
	preGap, err := s.Traverse(TraverseRequest{Start: "a", AsOf: vs["bc"], MaxDepth: 8, MaxVisited: 16})
	if err != nil {
		t.Fatal(err)
	}

	s.DeclareGap("link", "L/a/b", vs["ad"], Open)
	vAfterGap := s.CurrentVersion()

	res, err := s.Traverse(TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 8, MaxVisited: 16})
	if err == nil {
		t.Fatal("expected error")
	}
	if res != nil {
		t.Fatal("error must not return partial result")
	}
	if s.CurrentVersion() != vAfterGap {
		t.Fatal("failed traversal mutated store version")
	}
	// DeclareGap 不是历史事件，不应推进版本。
	if s.CurrentVersion() != vBefore {
		t.Fatal("DeclareGap mutated store version")
	}
	if len(s.Events()) != eventsBefore {
		t.Fatal("read-only operations appended to event log")
	}

	// 缺口之外的历史时刻不受影响，结果与声明前一致。
	good2, err := s.Traverse(TraverseRequest{Start: "a", AsOf: vs["bc"], MaxDepth: 8, MaxVisited: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preGap, good2) {
		t.Fatal("result outside gap changed after DeclareGap")
	}
}

// 同一历史时刻的多次遍历结果必须完全一致。
func TestRepeatedTraversalDeterministic(t *testing.T) {
	s, vs := buildChainStore(t)
	req := TraverseRequest{Start: "a", AsOf: vs["ad"], MaxDepth: 8, MaxVisited: 16}
	first, err := s.Traverse(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := s.Traverse(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs", i)
		}
	}
}

// 压实后的边界：边界之前不可回放，边界时刻基线状态完整。
func TestCompactionHorizon(t *testing.T) {
	s := NewStore()
	must := mkMust(t)
	must(s.DefineObjectType("T", []PropertyDef{{ID: "p", Name: "n", Type: TypeString}}))
	must(s.PutObject("T", "a", map[string]Value{"n": "x"}))
	v3 := must(s.SetProperty("a", "n", "y"))
	s.Compact(v3)

	if _, err := s.Traverse(TraverseRequest{Start: "a", AsOf: v3 - 1, MaxDepth: 1, MaxVisited: 4}); errKindOf(err) != ErrKindBeforeHorizon {
		t.Fatalf("got %v, want BEFORE_REPLAY_HORIZON", err)
	}
	res, err := s.Traverse(TraverseRequest{Start: "a", AsOf: v3, MaxDepth: 1, MaxVisited: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got := propsOf(t, res, "a")["n"]; got != "y" {
		t.Fatalf("baseline lost: got %v", got)
	}
}
