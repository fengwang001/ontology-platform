package ontology

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// buildStore 是测试用的建图辅助函数。
func buildStore(t *testing.T, links ...Link) *Store {
	t.Helper()
	s := NewStore()
	for _, l := range links {
		if err := s.AddLink(l); err != nil {
			t.Fatalf("AddLink(%v): %v", l.ID, err)
		}
	}
	return s
}

// render 把结果树渲染为确定性的文本，便于断言。
func render(n *Node) string {
	var b strings.Builder
	var walk func(cur *Node, indent string)
	walk = func(cur *Node, indent string) {
		for _, e := range cur.Edges {
			switch {
			case e.Kind == TermExtended:
				fmt.Fprintf(&b, "%s-%s[%s]-> %s\n", indent, e.LinkID, e.LinkLabel, e.To)
				walk(e.Child, indent+"  ")
			case e.Kind == TermCycle && e.Visible:
				fmt.Fprintf(&b, "%sCYCLE -%s[%s]-> %s\n", indent, e.LinkID, e.LinkLabel, e.To)
			case e.Kind == TermCycle:
				fmt.Fprintf(&b, "%sCYCLE(hidden)\n", indent)
			case e.Kind == TermHidden:
				fmt.Fprintf(&b, "%sHIDDEN-EXT\n", indent)
			}
		}
	}
	fmt.Fprintf(&b, "%s\n", n.Object)
	walk(n, "  ")
	return b.String()
}

// TestErrorOrder 验证四类错误互斥且按固定次序只报第一类。
func TestErrorOrder(t *testing.T) {
	// 图：A -l1[public]-> B；C 存在但只带 secret 链接。
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "C", To: "D", Label: "secret"},
	)
	s.AddObject("lonely")

	cases := []struct {
		name string
		req  Request
		want error
	}{
		// 起始对象不存在优先于其余一切错误。
		{"notfound_beats_all", Request{Start: "ghost", MaxDepth: 0}, ErrStartNotFound},
		{"notfound_beats_empty_labels", Request{Start: "ghost", CallerLabels: nil, MaxDepth: -1}, ErrStartNotFound},
		// 标签为空优先于深度非法与起始不可见。
		{"empty_labels_beats_depth", Request{Start: "A", MaxDepth: 0}, ErrEmptyCallerLabels},
		{"empty_labels_beats_invisible", Request{Start: "lonely", MaxDepth: -3}, ErrEmptyCallerLabels},
		// 深度非法优先于起始不可见。
		{"depth_beats_invisible_zero", Request{Start: "lonely", CallerLabels: []Label{"public"}, MaxDepth: 0}, ErrInvalidMaxDepth},
		{"depth_beats_invisible_negative", Request{Start: "lonely", CallerLabels: []Label{"public"}, MaxDepth: -2}, ErrInvalidMaxDepth},
		// 起始对象存在但所有关联链接的标签都不在调用方集合内。
		{"start_invisible", Request{Start: "C", CallerLabels: []Label{"public"}, MaxDepth: 5}, ErrStartInvisible},
		{"start_invisible_no_links", Request{Start: "lonely", CallerLabels: []Label{"public"}, MaxDepth: 5}, ErrStartInvisible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Traverse(s, tc.req, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestHiddenCycleTermination 验证仅由不可见链接闭合的真实环路：
// 必须终止扩展并给出环路标记，但不得泄露被隐藏链接的任何属性。
func TestHiddenCycleTermination(t *testing.T) {
	// A -l1[public]-> B -l2[secret]-> A：环路只有借助 secret 链接才能闭合。
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "B", To: "A", Label: "secret"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  -l1[public]-> B
    CYCLE(hidden)
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
	// 隐藏环路标记不得携带任何被隐藏链接的属性或连接对象标识。
	hidden := res.Root.Edges[0].Child.Edges[0]
	if hidden.Visible || hidden.LinkID != "" || hidden.LinkLabel != "" || hidden.To != "" || hidden.Child != nil {
		t.Fatalf("hidden cycle marker leaks link attributes: %+v", hidden)
	}
}

// TestVisibleCycleTermination 验证可见链接闭合的环路：
// 目标对象已在调用方可见路径上，可以指明。
func TestVisibleCycleTermination(t *testing.T) {
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "B", To: "A", Label: "public"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  -l1[public]-> B
    CYCLE -l2[public]-> A
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestHiddenAndCycleCoexist 验证「部分不可见」与「真实环路终止」
// 两类标记在同一次遍历中共存且互不混淆。
func TestHiddenAndCycleCoexist(t *testing.T) {
	// A -l1[public]-> B；
	// B -l2[secret]-> A（不可见闭合环路）；
	// B -l3[secret]-> C（不可见延伸，不成环）；
	// A -l4[public]-> D（完全可见的正常路径）。
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "B", To: "A", Label: "secret"},
		Link{ID: "l3", From: "B", To: "C", Label: "secret"},
		Link{ID: "l4", From: "A", To: "D", Label: "public"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  -l1[public]-> B
    CYCLE(hidden)
    HIDDEN-EXT
  -l4[public]-> D
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestVisiblePathUnaffected 验证完全可见且不成环的路径被原样返回，
// 深度上限截断属于正常返回，不产生额外可观察限制。
func TestVisiblePathUnaffected(t *testing.T) {
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "B", To: "C", Label: "public"},
		Link{ID: "l3", From: "C", To: "D", Label: "public"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  -l1[public]-> B
    -l2[public]-> C
      -l3[public]-> D
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
	// 深度上限为 2 时，C 节点不再扩展，但已返回的路径不得附带任何额外标记。
	res2, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want2 := `A
  -l1[public]-> B
    -l2[public]-> C
`
	if got := render(res2.Root); got != want2 {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want2)
	}
}

// TestSelfLoop 验证自环在完整图层面被判定为真实环路。
func TestSelfLoop(t *testing.T) {
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "A", Label: "public"},
		Link{ID: "l2", From: "A", To: "A", Label: "secret"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  CYCLE -l1[public]-> A
  CYCLE(hidden)
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestCallerConsistency 验证不同权限标签集合的调用方：
// 可见结果可以不同，但对同一条路径的环路终止判定必须一致。
func TestCallerConsistency(t *testing.T) {
	// A -l1[x]-> B -l2[y]-> C -l3[z]-> A（完整图上的真实环路）。
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "x"},
		Link{ID: "l2", From: "B", To: "C", Label: "y"},
		Link{ID: "l3", From: "C", To: "A", Label: "z"},
	)
	// 全量调用方：看到完整的可见环路标记。
	full, err := Traverse(s, Request{CallerLabels: []Label{"x", "y", "z"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantFull := `A
  -l1[x]-> B
    -l2[y]-> C
      CYCLE -l3[z]-> A
`
	if got := render(full.Root); got != wantFull {
		t.Fatalf("full caller tree mismatch:\ngot:\n%s\nwant:\n%s", got, wantFull)
	}
	// 受限调用方：看不到 l3，但在同一条路径上必须得到一致的环路终止判定。
	restricted, err := Traverse(s, Request{CallerLabels: []Label{"x", "y"}, Start: "A", MaxDepth: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantRestricted := `A
  -l1[x]-> B
    -l2[y]-> C
      CYCLE(hidden)
`
	if got := render(restricted.Root); got != wantRestricted {
		t.Fatalf("restricted caller tree mismatch:\ngot:\n%s\nwant:\n%s", got, wantRestricted)
	}
	// 交叉验证：两位调用方在共同可观察的路径前缀 A->B->C 上，
	// 对分支 C->A 的判定都必须是 TermCycle。
	fullC := full.Root.Edges[0].Child.Edges[0].Child
	restrictedC := restricted.Root.Edges[0].Child.Edges[0].Child
	if fullC.Edges[0].Kind != TermCycle || restrictedC.Edges[0].Kind != TermCycle {
		t.Fatalf("inconsistent cycle verdict: full=%v restricted=%v",
			fullC.Edges[0].Kind, restrictedC.Edges[0].Kind)
	}
	// 可见结果确实不同：受限调用方无法看到 l3 的任何属性。
	if restrictedC.Edges[0].Visible || restrictedC.Edges[0].LinkID != "" {
		t.Fatalf("restricted caller sees hidden link attributes: %+v", restrictedC.Edges[0])
	}
}

// TestHiddenMarkersDeduplicated 验证同一节点上的不可见标记只泄露存在性，
// 不泄露不可见链接的数量。
func TestHiddenMarkersDeduplicated(t *testing.T) {
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "h1", From: "A", To: "C", Label: "secret"},
		Link{ID: "h2", From: "A", To: "D", Label: "secret"},
		Link{ID: "h3", From: "A", To: "E", Label: "secret"},
	)
	res, err := Traverse(s, Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `A
  -l1[public]-> B
  HIDDEN-EXT
`
	if got := render(res.Root); got != want {
		t.Fatalf("tree mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
