package traverse

import (
	"errors"
	"reflect"
	"testing"

	"ontology/graph"
)

// buildStore 按 (对象列表, 链接列表{linkID, src, dst}) 构造图存储。
func buildStore(t *testing.T, objects []string, links [][3]string) *graph.Store {
	t.Helper()
	s := graph.NewStore()
	for _, id := range objects {
		if err := s.AddObject(graph.Object{ID: id, Type: "Thing"}); err != nil {
			t.Fatalf("AddObject %s: %v", id, err)
		}
	}
	for _, l := range links {
		if err := s.AddLink(graph.Link{ID: l[0], Type: "t", SourceID: l[1], TargetID: l[2]}); err != nil {
			t.Fatalf("AddLink %v: %v", l, err)
		}
	}
	return s
}

func markers(bs []Branch) []Marker {
	out := make([]Marker, len(bs))
	for i, b := range bs {
		out[i] = b.Marker
	}
	return out
}

func pathStrings(bs []Branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Path.String()
	}
	return out
}

func outReq(start string, depth, limit int) Request {
	return Request{
		StartID:     start,
		Directions:  []graph.Direction{graph.Outgoing},
		DepthLimit:  depth,
		ResultLimit: limit,
	}
}

// 仅深度截断：链 a->b->c->d，D=2，N 足够大。
func TestDepthOnly(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c", "d"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "b", "c"}, {"l3", "c", "d"},
	})
	res, err := NewService(s, nil).Traverse(outReq("a", 2, 10))
	if err != nil {
		t.Fatal(err)
	}
	if got := pathStrings(res.Returned); !reflect.DeepEqual(got, []string{"a-[outgoing:t:l1]->b-[outgoing:t:l2]->c"}) {
		t.Fatalf("returned paths = %v", got)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerDepthOnly}) {
		t.Fatalf("markers = %v", got)
	}
	if len(res.Truncated) != 0 {
		t.Fatalf("unexpected truncated: %v", pathStrings(res.Truncated))
	}
}

// 边界：深度上限恰好等于路径实际跳数。
// 链 a->b->c 实际 2 跳，D=2 时深度上限在路径末端恰好触发 -> depth_only；
// D=3 时路径在深度上限之前自然终结 -> none。
func TestDepthLimitExactlyEqualsPathHops(t *testing.T) {
	links := [][3]string{{"l1", "a", "b"}, {"l2", "b", "c"}}

	s := buildStore(t, []string{"a", "b", "c"}, links)
	res, err := NewService(s, nil).Traverse(outReq("a", 2, 10))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerDepthOnly}) {
		t.Fatalf("D==hops: markers = %v, want [depth_only]", got)
	}

	s2 := buildStore(t, []string{"a", "b", "c"}, links)
	res2, err := NewService(s2, nil).Traverse(outReq("a", 3, 10))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res2.Returned); !reflect.DeepEqual(got, []Marker{MarkerNone}) {
		t.Fatalf("D>hops: markers = %v, want [none]", got)
	}
}

// 仅条数截断：星形 a->b, a->c（均为叶子），D 足够大，N=1。
func TestLimitOnly(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "a", "c"},
	})
	res, err := NewService(s, nil).Traverse(outReq("a", 5, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerNone}) {
		t.Fatalf("returned markers = %v", got)
	}
	if got := pathStrings(res.Truncated); !reflect.DeepEqual(got, []string{"a-[outgoing:t:l2]->c"}) {
		t.Fatalf("truncated = %v", got)
	}
	if got := markers(res.Truncated); !reflect.DeepEqual(got, []Marker{MarkerLimitOnly}) {
		t.Fatalf("truncated markers = %v", got)
	}
}

// 深度先于条数：链 a->b->c->d，D=2，N=1。
// 唯一路径在第 2 跳触及深度上限，计入第 1 条结果时恰好使条数上限饱和。
// 这也是「条数上限恰好在路径完成判定瞬间被触及」的边界情形。
func TestDepthThenLimit(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c", "d"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "b", "c"}, {"l3", "c", "d"},
	})
	res, err := NewService(s, nil).Traverse(outReq("a", 2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerDepthThenLimit}) {
		t.Fatalf("markers = %v, want [depth_then_limit]", got)
	}
	if !res.Returned[0].Returned {
		t.Fatal("depth_then_limit 路径仍应被返回")
	}
	if len(res.Truncated) != 0 {
		t.Fatalf("unexpected truncated: %v", pathStrings(res.Truncated))
	}
}

// 条数先于深度：a->b（叶子）与 a->c->d->e->f（长链），D=3，N=1。
// a->b 计入后条数饱和，分支 a->c 被停止扩展；c 起 2 跳内本可触及深度上限。
func TestLimitThenDepth(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c", "d", "e", "f"}, [][3]string{
		{"l1", "a", "b"},
		{"l2", "a", "c"}, {"l3", "c", "d"}, {"l4", "d", "e"}, {"l5", "e", "f"},
	})
	res, err := NewService(s, nil).Traverse(outReq("a", 3, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerNone}) {
		t.Fatalf("returned markers = %v", got)
	}
	if got := pathStrings(res.Truncated); !reflect.DeepEqual(got, []string{"a-[outgoing:t:l2]->c"}) {
		t.Fatalf("truncated = %v", got)
	}
	if got := markers(res.Truncated); !reflect.DeepEqual(got, []Marker{MarkerLimitThenDepth}) {
		t.Fatalf("truncated markers = %v, want [limit_then_depth]", got)
	}
}

// 四种标记在同一次遍历中交叉出现。
// a->b->c（深度截断，计入时未饱和）-> depth_only
// a->d->e（深度截断，计入时恰好饱和）-> depth_then_limit
// a->f（叶子，条数饱和后被截断，剩余预算内到不了深度上限）-> limit_only
// a->g->h->i（条数饱和后被截断，剩余预算内本可触及深度上限）-> limit_then_depth
func TestAllFourMarkersInOneTraversal(t *testing.T) {
	s := buildStore(t,
		[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		[][3]string{
			{"l1", "a", "b"}, {"l2", "b", "c"},
			{"l3", "a", "d"}, {"l4", "d", "e"},
			{"l5", "a", "f"},
			{"l6", "a", "g"}, {"l7", "g", "h"}, {"l8", "h", "i"},
		})
	res, err := NewService(s, nil).Traverse(outReq("a", 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerDepthOnly, MarkerDepthThenLimit}) {
		t.Fatalf("returned markers = %v", got)
	}
	wantTruncPaths := []string{"a-[outgoing:t:l5]->f", "a-[outgoing:t:l6]->g"}
	if got := pathStrings(res.Truncated); !reflect.DeepEqual(got, wantTruncPaths) {
		t.Fatalf("truncated = %v, want %v", got, wantTruncPaths)
	}
	if got := markers(res.Truncated); !reflect.DeepEqual(got, []Marker{MarkerLimitOnly, MarkerLimitThenDepth}) {
		t.Fatalf("truncated markers = %v", got)
	}
}

// 边界：条数上限恰好等于终端路径总数，最后一条为自然终结。
// 所有路径返回、无截断分支；最后一条计入时上限恰好饱和但不产生截断标记。
func TestResultLimitExactlyAtLastNaturalCompletion(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "a", "c"},
	})
	res, err := NewService(s, nil).Traverse(outReq("a", 5, 2))
	if err != nil {
		t.Fatal(err)
	}
	if got := markers(res.Returned); !reflect.DeepEqual(got, []Marker{MarkerNone, MarkerNone}) {
		t.Fatalf("markers = %v, want [none none]", got)
	}
	if len(res.Truncated) != 0 {
		t.Fatalf("unexpected truncated: %v", pathStrings(res.Truncated))
	}
}

// 起始对象无可用链接：结果为空（返回条件为跳数 >= 1）。
func TestStartWithoutLinksYieldsEmpty(t *testing.T) {
	s := buildStore(t, []string{"a", "b"}, [][3]string{{"l1", "b", "a"}})
	res, err := NewService(s, nil).Traverse(outReq("a", 3, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Returned) != 0 || len(res.Truncated) != 0 {
		t.Fatalf("want empty result, got %+v", res)
	}
}

// 四类错误各自单独出现，且组合时按固定次序只报第一类。
func TestErrorPrecedence(t *testing.T) {
	s := buildStore(t, []string{"a"}, [][3]string{})
	svc := NewService(s, nil)

	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"start not found", outReq("zzz", 1, 1), ErrStartObjectNotFound},
		{"start not found beats all", Request{StartID: "zzz", DepthLimit: 0, ResultLimit: 0}, ErrStartObjectNotFound},
		{"bad depth", outReq("a", 0, 1), ErrInvalidDepthLimit},
		{"bad depth negative", outReq("a", -3, 1), ErrInvalidDepthLimit},
		{"bad depth beats bad limit and empty dirs", Request{StartID: "a", DepthLimit: 0, ResultLimit: 0}, ErrInvalidDepthLimit},
		{"bad limit", outReq("a", 1, 0), ErrInvalidResultLimit},
		{"bad limit negative", outReq("a", 1, -2), ErrInvalidResultLimit},
		{"bad limit beats empty dirs", Request{StartID: "a", DepthLimit: 1, ResultLimit: 0}, ErrInvalidResultLimit},
		{"empty directions", Request{StartID: "a", DepthLimit: 1, ResultLimit: 1}, ErrEmptyDirections},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := svc.Traverse(c.req)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if res != nil {
				t.Fatalf("被拒绝的请求不得产生部分结果, got %+v", res)
			}
		})
	}
}

// 同一输入多次执行得到完全一致的截断标记分布。
func TestDeterministicAcrossRuns(t *testing.T) {
	s := buildStore(t,
		[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		[][3]string{
			{"l1", "a", "b"}, {"l2", "b", "c"},
			{"l3", "a", "d"}, {"l4", "d", "e"},
			{"l5", "a", "f"},
			{"l6", "a", "g"}, {"l7", "g", "h"}, {"l8", "h", "i"},
			{"l9", "c", "a"}, // 回边，增加复杂度
		})
	svc := NewService(s, nil)
	req := outReq("a", 3, 3)
	first, err := svc.Traverse(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := svc.Traverse(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs:\nfirst=%+v\ngot=%+v", i, first, got)
		}
	}
}
