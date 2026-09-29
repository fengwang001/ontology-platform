package lineage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

func rref(id string, v int64) ObjectRef { return ObjectRef{ID: id, Version: v} }

func mustRecord(t *testing.T, tr *Tracker, op string, in, out []ObjectRef) {
	t.Helper()
	if err := tr.RecordDerivation(context.Background(), Record{Operation: op, Inputs: in, Outputs: out}); err != nil {
		t.Fatalf("RecordDerivation(%s) unexpected error: %v", op, err)
	}
}

func mustRegister(t *testing.T, tr *Tracker, r ObjectRef) {
	t.Helper()
	if err := tr.RegisterSource(context.Background(), r); err != nil {
		t.Fatalf("RegisterSource(%v) unexpected error: %v", r, err)
	}
}

func edgeKey(e Edge) string {
	flag := ""
	if e.Invalid {
		flag = "!"
	}
	return fmt.Sprintf("%s%d->%s%d:%s%s", e.From.ID, e.From.Version, e.To.ID, e.To.Version, e.Operation, flag)
}

// graphSignature 将血缘图压缩成与遍历/发生顺序无关的稳定签名。
func graphSignature(g *Graph) string {
	s := "nodes:"
	for _, n := range g.Nodes {
		s += fmt.Sprintf(" %s@%d", n.ID, n.Version)
	}
	s += "|edges:"
	for _, e := range g.Edges {
		s += " " + edgeKey(e)
	}
	return s
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// TestRecordAndBidirectionalTrace 验证操作时记录血缘，且上游/下游双向可追溯。
func TestRecordAndBidirectionalTrace(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTrackerWithLogger(&buf)
	ctx := context.Background()

	mustRegister(t, tr, rref("a", 1))
	mustRecord(t, tr, "join", []ObjectRef{rref("a", 1)}, []ObjectRef{rref("b", 1)})
	mustRecord(t, tr, "enrich", []ObjectRef{rref("b", 1)}, []ObjectRef{rref("c", 1)})
	mustRecord(t, tr, "publish", []ObjectRef{rref("c", 1)}, []ObjectRef{rref("d", 1)})

	up, err := tr.Upstream(ctx, rref("c", 1))
	if err != nil {
		t.Fatalf("Upstream(c) error: %v", err)
	}
	if got, want := graphSignature(up),
		"nodes: a@1 b@1 c@1|edges: a1->b1:join b1->c1:enrich"; got != want {
		t.Fatalf("upstream graph = %q, want %q", got, want)
	}

	down, err := tr.Downstream(ctx, rref("b", 1))
	if err != nil {
		t.Fatalf("Downstream(b) error: %v", err)
	}
	if got, want := graphSignature(down),
		"nodes: b@1 c@1 d@1|edges: b1->c1:enrich c1->d1:publish"; got != want {
		t.Fatalf("downstream graph = %q, want %q", got, want)
	}

	up2, down2, err := tr.Lineage(ctx, rref("c", 1))
	if err != nil {
		t.Fatalf("Lineage(c) error: %v", err)
	}
	if len(up2.Edges) != 2 || len(down2.Edges) != 1 {
		t.Fatalf("Lineage(c) edges = up %d, down %d, want 2/1", len(up2.Edges), len(down2.Edges))
	}

	upSrc, err := tr.Upstream(ctx, rref("a", 1))
	if err != nil {
		t.Fatalf("Upstream(source) error: %v", err)
	}
	if len(upSrc.Edges) != 0 || len(upSrc.Nodes) != 1 {
		t.Fatalf("source upstream = %+v, want empty graph with only root", upSrc)
	}

	log := buf.String()
	for _, want := range []string{"derivation recorded", "lineage query ok", "join", "enrich"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q; log:\n%s", want, log)
		}
	}
}

// TestInvalidRecordRejected 派生无血缘记录必须拒绝，原因可区分，且状态不变。
func TestInvalidRecordRejected(t *testing.T) {
	tr := NewTrackerWithLogger(nil)
	ctx := context.Background()
	mustRegister(t, tr, rref("a", 1))

	cases := []struct {
		name string
		rec  Record
		want error
	}{
		{"empty operation", Record{Inputs: []ObjectRef{rref("a", 1)}, Outputs: []ObjectRef{rref("b", 1)}}, ErrInvalidRecord},
		{"no inputs", Record{Operation: "x", Outputs: []ObjectRef{rref("b", 1)}}, ErrInvalidRecord},
		{"no outputs", Record{Operation: "x", Inputs: []ObjectRef{rref("a", 1)}}, ErrInvalidRecord},
		{"input and output same id", Record{Operation: "x", Inputs: []ObjectRef{rref("a", 1)}, Outputs: []ObjectRef{rref("a", 2)}}, ErrInvalidRecord},
		{"unknown input", Record{Operation: "x", Inputs: []ObjectRef{rref("z", 1)}, Outputs: []ObjectRef{rref("b", 1)}}, ErrUnknownVersion},
		{"new output not v1", Record{Operation: "x", Inputs: []ObjectRef{rref("a", 1)}, Outputs: []ObjectRef{rref("b", 3)}}, ErrUnknownVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertErrorIs(t, tr.RecordDerivation(ctx, tc.rec), tc.want)
		})
	}

	if _, err := tr.Downstream(ctx, rref("a", 1)); !errors.Is(err, ErrNoDownstream) {
		t.Fatalf("after rejections, a should have no downstream, got %v", err)
	}
	mustRecord(t, tr, "first", []ObjectRef{rref("a", 1)}, []ObjectRef{rref("b", 1)})
}

// TestMissingUpstreamDownstream 查询漏上游或漏下游必须拒绝，原因可区分。
func TestMissingUpstreamDownstream(t *testing.T) {
	tr := NewTrackerWithLogger(nil)
	ctx := context.Background()
	mustRegister(t, tr, rref("a", 1))
	mustRecord(t, tr, "build", []ObjectRef{rref("a", 1)}, []ObjectRef{rref("b", 1)})

	_, err := tr.Downstream(ctx, rref("b", 1))
	assertErrorIs(t, err, ErrNoDownstream)

	if _, err := tr.Upstream(ctx, rref("a", 1)); err != nil {
		t.Fatalf("source upstream should be empty graph, got %v", err)
	}
	upA, downA, err := tr.Lineage(ctx, rref("a", 1))
	if err != nil {
		t.Fatalf("Lineage(source) error: %v", err)
	}
	if len(upA.Edges) != 0 || len(downA.Edges) != 1 {
		t.Fatalf("Lineage(source) = up %d edges, down %d edges, want 0/1", len(upA.Edges), len(downA.Edges))
	}

	_, _, err = tr.Lineage(ctx, rref("b", 1))
	assertErrorIs(t, err, ErrNoDownstream)

	// 白盒构造一个“无生产记录的派生对象”，触发 ErrNoUpstream。
	tr.mu.Lock()
	tr.state.current["orphan"] = 1
	tr.state.sources["orphan"] = false
	tr.mu.Unlock()
	_, err = tr.Upstream(ctx, rref("orphan", 1))
	assertErrorIs(t, err, ErrNoUpstream)
}

// TestStaleVersionInvalidation 版本推进后旧血缘失效，查询过期版本被拒绝。
func TestStaleVersionInvalidation(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTrackerWithLogger(&buf)
	ctx := context.Background()
	mustRegister(t, tr, rref("a", 1))
	mustRecord(t, tr, "v1-derive", []ObjectRef{rref("a", 1)}, []ObjectRef{rref("b", 1)})

	mustRegister(t, tr, rref("a", 2))

	if _, err := tr.Downstream(ctx, rref("a", 1)); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("Downstream(a@1) = %v, want ErrStaleVersion", err)
	}
	if _, err := tr.Upstream(ctx, rref("b", 1)); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("Upstream(b@1) = %v, want ErrStaleVersion", err)
	}
	if _, err := tr.Downstream(ctx, rref("a", 9)); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Downstream(a@9) = %v, want ErrUnknownVersion", err)
	}

	err := tr.RecordDerivation(ctx, Record{
		Operation: "stale-in",
		Inputs:    []ObjectRef{rref("a", 1)},
		Outputs:   []ObjectRef{rref("c", 1)},
	})
	assertErrorIs(t, err, ErrStaleVersion)

	mustRecord(t, tr, "v2-derive", []ObjectRef{rref("a", 2)}, []ObjectRef{rref("c", 1)})
	g, err := tr.Downstream(ctx, rref("a", 2))
	if err != nil {
		t.Fatalf("Downstream(a@2) error: %v", err)
	}
	if len(g.Edges) != 1 || g.Edges[0].Invalid {
		t.Fatalf("a@2 downstream edges = %+v, want one valid edge", g.Edges)
	}

	mustRecord(t, tr, "upgrade-b", []ObjectRef{rref("a", 2)}, []ObjectRef{rref("b", 2)})
	// b 升级到 v2 后，最早的 a@1->b@1 已失效，追溯 b@1 上游必然命中过期血缘。
	if _, err := tr.Upstream(ctx, rref("b", 1)); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("Upstream(b@1) after b upgraded = %v, want ErrStaleVersion", err)
	}
	// b@1 也不能再作为派生输入。
	err = tr.RecordDerivation(ctx, Record{Operation: "use-b1", Inputs: []ObjectRef{rref("b", 1)}, Outputs: []ObjectRef{rref("d", 1)}})
	assertErrorIs(t, err, ErrStaleVersion)
	mustRecord(t, tr, "use-b2", []ObjectRef{rref("b", 2)}, []ObjectRef{rref("d", 1)})
	g2, err := tr.Downstream(ctx, rref("b", 2))
	if err != nil {
		t.Fatalf("Downstream(b@2) error: %v", err)
	}
	if len(g2.Edges) != 1 || edgeKey(g2.Edges[0]) != "b2->d1:use-b2" {
		t.Fatalf("b@2 downstream = %+v, want single b2->d1 edge", g2.Edges)
	}

	if !bytes.Contains(buf.Bytes(), []byte("invalidated")) {
		t.Fatalf("log missing invalidation evidence:\n%s", buf.String())
	}
}

// TestFanInFanOutGraph 验证多输入多输出的完整血缘（每对 输入×输出 一条边）。
func TestFanInFanOutGraph(t *testing.T) {
	tr := NewTrackerWithLogger(nil)
	ctx := context.Background()
	mustRegister(t, tr, rref("x", 1))
	mustRegister(t, tr, rref("y", 1))
	mustRecord(t, tr, "merge",
		[]ObjectRef{rref("x", 1), rref("y", 1)},
		[]ObjectRef{rref("p", 1), rref("q", 1)})
	mustRecord(t, tr, "split", []ObjectRef{rref("p", 1)}, []ObjectRef{rref("r", 1), rref("s", 1)})

	up, err := tr.Upstream(ctx, rref("r", 1))
	if err != nil {
		t.Fatalf("Upstream(r): %v", err)
	}
	// r <- p <- merge(x,y)：祖先是 r,p,x,y，共 3 条边（q 不是 r 的祖先）。
	if len(up.Edges) != 3 {
		t.Fatalf("upstream r edges = %d, want 3: %v", len(up.Edges), up.Edges)
	}

	down, err := tr.Downstream(ctx, rref("x", 1))
	if err != nil {
		t.Fatalf("Downstream(x): %v", err)
	}
	if len(down.Edges) != 4 {
		t.Fatalf("downstream x edges = %d, want 4: %v", len(down.Edges), down.Edges)
	}
}
