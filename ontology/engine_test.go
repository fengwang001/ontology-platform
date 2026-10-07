package ontology

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

func mustCommit(t *testing.T, rep Report, err *ActionError) {
	t.Helper()
	if err != nil || !rep.Committed {
		t.Fatalf("expected commit, got err=%v rep=%+v", err, rep)
	}
}

func mustReject(t *testing.T, want ErrorKind, rep Report, err *ActionError) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection kind %d, got commit", want)
	}
	if err.Kind != want {
		t.Fatalf("expected kind %d, got %d (%s)", want, err.Kind, err.Msg)
	}
	if rep.Committed {
		t.Fatalf("rejected action must not commit")
	}
}

func mustGet(t *testing.T, st *MemStore, id string) Instance {
	t.Helper()
	in, ok := st.Get(InstanceID(id))
	if !ok {
		t.Fatalf("instance %s missing", id)
	}
	return in
}

func statesEqual(a, b WorldState) bool {
	if a.Clock != b.Clock || len(a.Instances) != len(b.Instances) ||
		len(a.Edges) != len(b.Edges) || len(a.Audit) != len(b.Audit) {
		return false
	}
	for id, in := range a.Instances {
		other, ok := b.Instances[id]
		if !ok || !instanceEqual(other, in) {
			return false
		}
	}
	ea := edgeSet(a.Edges)
	eb := edgeSet(b.Edges)
	if len(ea) != len(eb) {
		return false
	}
	for k := range ea {
		if eb[k] == 0 {
			return false
		}
	}
	return true
}

func instanceEqual(x, y Instance) bool {
	if x.ID != y.ID || x.Type != y.Type || x.Version != y.Version || len(x.Attrs) != len(y.Attrs) {
		return false
	}
	for k, v := range x.Attrs {
		if y.Attrs[k] != v {
			return false
		}
	}
	return true
}

func edgeSet(edges []Edge) map[Edge]int {
	m := map[Edge]int{}
	for _, e := range edges {
		m[e]++
	}
	return m
}

func TestInvalidDeclaration(t *testing.T) {
	st := mkStore(nil, nil)
	eng := NewEngine(st, newTestDecider(), nil)
	_, err := eng.Execute(ActionDeclaration{Name: "", Subject: "s"})
	mustReject(t, RejectInvalidDeclaration, Report{}, err)
	_, err = eng.Execute(ActionDeclaration{Name: "a", Subject: "s", InvisibleMode: 7})
	mustReject(t, RejectInvalidDeclaration, Report{}, err)
	if st.Clock() != 0 {
		t.Fatalf("rejected declaration must not tick clock: %d", st.Clock())
	}
}

func TestDepthBoundary(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc"), inst("D", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C"), edge("rel", "C", "D")}
	d := newTestDecider()
	for _, id := range []string{"A", "B", "C", "D"} {
		d.allowAll(InstanceID(id), OpUpdate)
	}

	st := mkStore(insts, edges)
	eng := NewEngine(st, d, nil)
	a := baseAction("A")
	a.MaxDepth = 2
	_, err := eng.Execute(a)
	mustReject(t, RejectDepthExceeded, Report{}, err)

	st2 := mkStore(insts, edges)
	eng2 := NewEngine(st2, d, nil)
	a.MaxDepth = 3
	rep, err := eng2.Execute(a)
	mustCommit(t, rep, err)
	if v := mustGet(t, st2, "D").Version; v != 2 {
		t.Fatalf("D at exact depth 3 should update, version=%d", v)
	}

	a2 := baseAction("A")
	a2.MaxDepth = 2
	a2.Cascades = []CascadeRule{{LinkType: "rel", Outgoing: true, Effect: OpUpdate, NoPropagate: true}}
	st3 := mkStore(insts, edges)
	eng3 := NewEngine(st3, d, nil)
	rep, err = eng3.Execute(a2)
	mustCommit(t, rep, err)
	if v := mustGet(t, st3, "D").Version; v != 1 {
		t.Fatalf("D beyond NoPropagate edge must be untouched, version=%d", v)
	}
}

func TestCycleDedup(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C"), edge("rel", "C", "A")}
	d := newTestDecider()
	for _, id := range []string{"A", "B", "C"} {
		d.allowAll(InstanceID(id), OpUpdate)
	}
	st := mkStore(insts, edges)
	eng := NewEngine(st, d, nil)
	a := baseAction("A")
	a.MaxDepth = 10
	rep, err := eng.Execute(a)
	mustCommit(t, rep, err)

	seen := map[InstanceID]int{}
	for _, p := range rep.Path {
		seen[p.Instance]++
		if seen[p.Instance] > 1 {
			t.Fatalf("instance %s checked more than once", p.Instance)
		}
	}
	if len(rep.Path) != 3 {
		t.Fatalf("path entries = %d want 3", len(rep.Path))
	}
	for _, id := range []string{"A", "B", "C"} {
		if v := mustGet(t, st, id).Version; v != 2 {
			t.Fatalf("%s version=%d want 2", id, v)
		}
	}
}

func TestInvisibleDirectVsCascadePriority(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C")}
	d := newTestDecider()
	d.allowAll("B", OpUpdate)
	d.allowAll("C", OpUpdate)
	d.setInvisible("A")
	d.setInvisible("B")
	eng := NewEngine(mkStore(insts, edges), d, nil)
	_, err := eng.Execute(baseAction("A"))
	mustReject(t, RejectDirectInvisible, Report{}, err)
}

func TestInvisibleCascadeRejectModeLeavesStateUntouched(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C")}
	d := newTestDecider()
	d.allowAll("A", OpUpdate)
	d.setInvisible("B")
	st := mkStore(insts, edges)
	before := st.Snapshot()
	eng := NewEngine(st, d, nil)
	_, err := eng.Execute(baseAction("A"))
	mustReject(t, RejectCascadeInvisible, Report{}, err)
	if !statesEqual(before, st.Snapshot()) {
		t.Fatalf("reject-on-invisible must not change any observable state")
	}
}

func TestInvisibleCascadeSkipMode(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc"), inst("D", "doc")}
	edges := []Edge{
		edge("rel", "A", "B"), edge("rel", "B", "C"), edge("rel", "A", "D"),
	}
	d := newTestDecider()
	for _, id := range []string{"A", "C", "D"} {
		d.allowAll(InstanceID(id), OpUpdate)
	}
	d.setInvisible("B")
	st := mkStore(insts, edges)
	eng := NewEngine(st, d, nil)
	a := baseAction("A")
	a.InvisibleMode = SkipOnInvisible
	rep, err := eng.Execute(a)
	mustCommit(t, rep, err)

	if v := mustGet(t, st, "B").Version; v != 1 {
		t.Fatalf("skipped B changed, version=%d", v)
	}
	if v := mustGet(t, st, "C").Version; v != 1 {
		t.Fatalf("C behind skipped B must be untouched, version=%d", v)
	}
	if v := mustGet(t, st, "D").Version; v != 2 {
		t.Fatalf("D via independent path should update, version=%d", v)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Instance != "B" {
		t.Fatalf("skip record mismatch: %+v", rep.Skipped)
	}
	// 传播不从不可见的 B 扩展，因此 C 的存在不被记录（避免泄露）；
	// 剪除范围精确为 1（仅可确认存在且不可见的 B）。
	if rep.Skipped[0].PrunedSubtree != 1 {
		t.Fatalf("pruned subtree = %d want 1", rep.Skipped[0].PrunedSubtree)
	}
	for _, s := range rep.Skipped {
		if len(s.WithheldEffects) != 1 || s.WithheldEffects[0] != OpUpdate {
			t.Fatalf("skip record leaked/restricted wrong op info: %+v", s)
		}
	}
	aud := st.Audit()
	if len(aud) != 1 || len(aud[0].Skipped) != 1 {
		t.Fatalf("audit mismatch: %+v", aud)
	}
	for _, eff := range aud[0].Applied {
		if eff.Target == "B" || eff.Target == "C" {
			t.Fatalf("audit lists withheld effect for %s", eff.Target)
		}
	}
}

func TestMergePolicies(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc")}
	edges := []Edge{edge("rel", "A", "B")}
	// 两个层级的票：根 A 在层级 0 拒绝；级联 B 在层级 1 放行。
	d := newTestDecider()
	d.allow("A", OpUpdate, 0, false)
	d.allow("B", OpUpdate, 1, true)

	st := mkStore(insts, edges)
	a := baseAction("A")
	a.Merge = MergeAll
	_, err := NewEngine(st, d, nil).Execute(a)
	mustReject(t, RejectDenied, Report{}, err)

	st2 := mkStore(insts, edges)
	a.Merge = MergeAny
	rep, err := NewEngine(st2, d, nil).Execute(a)
	mustCommit(t, rep, err)
	if v := mustGet(t, st2, "B").Version; v != 2 {
		t.Fatalf("MergeAny should cascade, B version=%d", v)
	}

	// 全部弃权：两种策略都拒绝。
	d2 := newTestDecider()
	st3 := mkStore(insts, edges)
	a2 := baseAction("A")
	a2.Merge = MergeAny
	_, err = NewEngine(st3, d2, nil).Execute(a2)
	mustReject(t, RejectDenied, Report{}, err)
}

func TestMergeOrderIndependence(t *testing.T) {
	ds := []LayerDecision{
		{Depth: 2, Allowed: false}, {Depth: 0, Allowed: true}, {Depth: 1, Allowed: true},
	}
	if mergeDecisions(MergeAll, ds) {
		t.Fatalf("MergeAll with a deny must be false regardless of order")
	}
	ds2 := []LayerDecision{
		{Depth: 0, Allowed: false}, {Depth: 2, Allowed: true}, {Depth: 1, Allowed: false},
	}
	if !mergeDecisions(MergeAny, ds2) {
		t.Fatalf("MergeAny with a permit must be true regardless of order")
	}
}

func TestAtomicRollbackOnDenial(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C")}
	d := newTestDecider()
	d.allowAll("A", OpUpdate)
	d.allowAll("B", OpUpdate)
	d.allow("C", OpUpdate, 2, false)
	st := mkStore(insts, edges)
	before := st.Snapshot()
	_, err := NewEngine(st, d, nil).Execute(baseAction("A"))
	mustReject(t, RejectDenied, Report{}, err)
	if !statesEqual(before, st.Snapshot()) {
		t.Fatalf("denied action changed state")
	}
	if len(st.Audit()) != 0 || st.Clock() != 0 {
		t.Fatalf("denied action must not write audit or tick clock")
	}
}

func TestAtomicCommit(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc")}
	edges := []Edge{edge("rel", "A", "B")}
	d := newTestDecider()
	d.allowAll("A", OpUpdate)
	d.allowAll("B", OpUpdate)
	st := mkStore(insts, edges)
	rep, err := NewEngine(st, d, nil).Execute(baseAction("A"))
	mustCommit(t, rep, err)
	if st.Clock() != 1 || len(st.Audit()) != 1 {
		t.Fatalf("clock=%d audit=%d want 1/1", st.Clock(), len(st.Audit()))
	}
}

func TestErrorPriorityOrdering(t *testing.T) {
	insts := []Instance{
		inst("A", "doc"), inst("B", "doc"), inst("C", "doc"), inst("D", "doc"), inst("E", "doc"),
	}
	edges := []Edge{
		edge("rel", "A", "B"), edge("rel", "B", "C"), edge("rel", "C", "D"), edge("rel", "D", "E"),
	}
	d := newTestDecider()
	d.setInvisible("A") // 直接目标不可见
	d.setInvisible("B") // 级联不可见
	st := mkStore(insts, edges)
	a := baseAction("A")
	a.MaxDepth = 2 // 同时越界
	_, err := NewEngine(st, d, nil).Execute(a)
	// 深度越界优先于可见性。
	mustReject(t, RejectDepthExceeded, Report{}, err)

	d2 := newTestDecider()
	d2.setInvisible("A")
	d2.setInvisible("B")
	st2 := mkStore(insts, edges)
	a2 := baseAction("A")
	a2.MaxDepth = 5
	_, err = NewEngine(st2, d2, nil).Execute(a2)
	mustReject(t, RejectDirectInvisible, Report{}, err)

	d3 := newTestDecider()
	d3.allowAll("A", OpUpdate)
	d3.setInvisible("B")
	st3 := mkStore(insts, edges)
	a3 := baseAction("A")
	a3.MaxDepth = 5
	_, err = NewEngine(st3, d3, nil).Execute(a3)
	mustReject(t, RejectCascadeInvisible, Report{}, err)
}

func TestJSONLoggerRecordsInputPathOutput(t *testing.T) {
	insts := []Instance{inst("A", "doc"), inst("B", "doc")}
	edges := []Edge{edge("rel", "A", "B")}
	d := newTestDecider()
	d.allowAll("A", OpUpdate)
	d.allowAll("B", OpUpdate)
	var buf bytes.Buffer
	st := mkStore(insts, edges)
	eng := NewEngine(st, d, NewJSONLogger(&buf))
	rep, err := eng.Execute(baseAction("A"))
	mustCommit(t, rep, err)
	if buf.Len() == 0 {
		t.Fatalf("logger wrote nothing")
	}
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log is not valid JSON: %v\n%s", err, buf.String())
	}
	if entry["committed"] != true {
		t.Fatalf("log missing committed=true")
	}
	if _, ok := entry["input"]; !ok {
		t.Fatalf("log missing input")
	}
	if _, ok := entry["path"]; !ok {
		t.Fatalf("log missing adjudication path")
	}
	if _, ok := entry["state_before"]; !ok {
		t.Fatalf("log missing state_before")
	}
}

func TestConcurrentSerializability(t *testing.T) {
	// N 个 goroutine 反复对重叠链接图提交动作；最终时钟必须恰好等于
	// 成功提交次数，且审计序号从 1 连续无缺——等价于某个串行顺序。
	insts := []Instance{inst("A", "doc"), inst("B", "doc"), inst("C", "doc")}
	edges := []Edge{edge("rel", "A", "B"), edge("rel", "B", "C")}
	d := newTestDecider()
	for _, id := range []string{"A", "B", "C"} {
		d.allowAll(InstanceID(id), OpUpdate)
	}
	st := mkStore(insts, edges)
	eng := NewEngine(st, d, nil)

	const goroutines, iterations = 8, 25
	var wg sync.WaitGroup
	var commits int64
	var mu sync.Mutex
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			local := 0
			for i := 0; i < iterations; i++ {
				a := baseAction("A")
				a.Name = "concurrent"
				rep, err := eng.Execute(a)
				if err == nil && rep.Committed {
					local++
				}
			}
			mu.Lock()
			commits += int64(local)
			mu.Unlock()
		}(g)
	}
	wg.Wait()

	if st.Clock() != commits {
		t.Fatalf("clock=%d commits=%d", st.Clock(), commits)
	}
	if int64(len(st.Audit())) != commits {
		t.Fatalf("audit records=%d commits=%d", len(st.Audit()), commits)
	}
	for i, rec := range st.Audit() {
		if rec.Seq != int64(i+1) {
			t.Fatalf("audit seq gap at %d: %d", i, rec.Seq)
		}
	}
	// 所有成功提交都产生同样的最终版本：初始 1 + commits。
	for _, id := range []string{"A", "B", "C"} {
		if v := mustGet(t, st, id).Version; v != 1+int(commits) {
			t.Fatalf("%s version=%d want %d", id, v, 1+commits)
		}
	}
}
