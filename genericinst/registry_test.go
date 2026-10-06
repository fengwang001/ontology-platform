package genericinst

import (
	"sort"
	"strings"
	"sync"
	"testing"
)

// ---------- 测试辅助 ----------

func mustReq(t *testing.T, r *Registry, unit, def string, args []Type, body Body) *Instance {
	t.Helper()
	in, e := r.Request(unit, def, args, body)
	if e != nil {
		t.Fatalf("unexpected error %s for %s%v", e.Msg, def, args)
	}
	return in
}

func errCode(e *RequestError) ErrorCode {
	if e == nil {
		return 0
	}
	return e.Code
}

func validIDs(r *Registry) []int {
	out := []int{}
	for id, in := range r.instances {
		if in.stale == nil {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// ---------- 1. 三种等价写法必须命中同一实例 ----------

func TestEquivalenceAliasStructDefault(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "Box", Params: []Param{
		{Name: "T"},
		{Name: "U", Default: NewNominal("int")},
	}})

	// 别名与目标等价。
	alias := NewAlias("MyInt", NewNominal("int"))
	a := mustReq(t, r, "u1", "Box", []Type{NewNominal("int")}, nil)
	b := mustReq(t, r, "u2", "Box", []Type{alias}, nil)
	if a.ID != b.ID {
		t.Fatalf("alias should hit same instance: %d vs %d", a.ID, b.ID)
	}

	// 省略带默认值形参，与显式写默认值等价。
	c := mustReq(t, r, "u3", "Box", []Type{NewNominal("int"), NewNominal("int")}, nil)
	if c.ID != a.ID {
		t.Fatalf("default param should hit same instance: %d vs %d", c.ID, a.ID)
	}

	// 结构类型：同字段名/次序/类型才等价。
	s1 := NewStruct([]StructField{{Name: "x", Type: NewNominal("int")}, {Name: "y", Type: alias}})
	s2 := NewStruct([]StructField{{Name: "x", Type: alias}, {Name: "y", Type: NewNominal("int")}})
	s3 := NewStruct([]StructField{{Name: "y", Type: NewNominal("int")}, {Name: "x", Type: NewNominal("int")}})
	d := mustReq(t, r, "u1", "Box", []Type{s1}, nil)
	e := mustReq(t, r, "u2", "Box", []Type{s2}, nil)
	f := mustReq(t, r, "u3", "Box", []Type{s3}, nil)
	if d.ID != e.ID {
		t.Fatalf("struct via alias should hit same instance")
	}
	if f.ID == d.ID {
		t.Fatalf("struct with different field order must NOT be merged")
	}

	snap := r.SnapshotView()
	if snap.ValidCount != 3 { // Box[int,int], Box[struct{x,y}], Box[struct{y,x}]
		t.Fatalf("want 3 valid instances, got %d", snap.ValidCount)
	}
	if snap.TotalCreates != 3 || snap.TotalHits != 3 {
		t.Fatalf("want creates=3 hits=3, got %d/%d", snap.TotalCreates, snap.TotalHits)
	}
}

// 未指定且无默认值的形参 -> 参数错误。
func TestMissingRequiredArg(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "Pair", Params: []Param{{Name: "A"}, {Name: "B"}}})
	_, e := r.Request("u1", "Pair", []Type{NewNominal("int")}, nil)
	if errCode(e) != ErrArgument {
		t.Fatalf("want argument error, got %v", e)
	}
}

// ---------- 4. 约束与固定拒绝次序 ----------

func TestConstraints(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "Num", Params: []Param{
		{Name: "T", Con: &Constraint{OneOf: []string{"int", "float"}}},
	}})
	if _, e := r.Request("u1", "Num", []Type{NewNominal("bool")}, nil); errCode(e) != ErrConstraint {
		t.Fatalf("OneOf violation must be constraint error, got %v", e)
	}
	mustReq(t, r, "u1", "Num", []Type{NewNominal("float")}, nil)

	r.RegisterDef(&Def{Name: "Eq", Params: []Param{
		{Name: "A"},
		{Name: "B", Con: &Constraint{SameAs: 0}},
	}})
	if _, e := r.Request("u1", "Eq", []Type{NewNominal("int"), NewNominal("bool")}, nil); errCode(e) != ErrConstraint {
		t.Fatalf("SameAs violation must be constraint error, got %v", e)
	}
	x := mustReq(t, r, "u1", "Eq", []Type{NewNominal("int"), NewNominal("int")}, nil)
	y := mustReq(t, r, "u2", "Eq",
		[]Type{NewAlias("a", NewNominal("int")), NewNominal("int")}, nil)
	if x.ID != y.ID {
		t.Fatalf("equivalent constrained args must hit same instance")
	}
}

func TestRejectionOrder(t *testing.T) {
	// 未定义优先于一切（即使同时参数过多、深度/配额受限）。
	if _, e := New().Request("u1", "Nope", []Type{NewNominal("a"), NewNominal("b")}, nil); errCode(e) != ErrUndefined {
		t.Fatalf("undefined must win, got %v", e)
	}

	// 参数错误优先于约束错误。
	r := New()
	r.RegisterDef(&Def{Name: "P", Params: []Param{
		{Name: "T", Con: &Constraint{OneOf: []string{"int"}}},
	}})
	if _, e := r.Request("u1", "P", []Type{NewNominal("bool"), NewNominal("bool")}, nil); errCode(e) != ErrArgument {
		t.Fatalf("argument must win over constraint, got %v", e)
	}

	// 约束优先于深度：到达深层时实参违反约束（同层也会超深度）。
	rc := New(WithMaxDepth(1))
	rc.RegisterDef(&Def{Name: "C", Params: []Param{{Name: "T", Con: &Constraint{OneOf: []string{"int"}}}}})
	_, e := rc.Request("u1", "C", []Type{NewNominal("int")}, func(s *Session) error {
		_, e2 := s.Request("C", []Type{NewNominal("bad")}, nil)
		return e2
	})
	if errCode(e) != ErrConstraint {
		t.Fatalf("constraint must win over depth, got %v", e)
	}

	// 深度优先于配额：嵌套到达深度上限时不做任何配额扣减，报深度错误。
	rd := New(WithMaxDepth(1), WithMaxInstances(100))
	rd.RegisterDef(&Def{Name: "D", Params: []Param{{Name: "T"}}})
	_, e = rd.Request("u1", "D", []Type{NewNominal("int")}, func(s *Session) error {
		_, e2 := s.Request("D", []Type{NewNominal("string")}, nil)
		return e2
	})
	if errCode(e) != ErrDepth {
		t.Fatalf("depth must win over quota, got %v", e)
	}

	// 配额最后：未触发任何更高级错误但配额已满。
	rq := New(WithMaxInstances(1))
	rq.RegisterDef(&Def{Name: "D", Params: []Param{{Name: "T"}}})
	mustReq(t, rq, "u0", "D", []Type{NewNominal("int")}, nil)
	if _, e := rq.Request("u1", "D", []Type{NewNominal("string")}, nil); errCode(e) != ErrQuota {
		t.Fatalf("quota must be last resort, got %v", e)
	}
}

// ---------- 5. 配额取等边界 ----------

func TestQuotaBoundaries(t *testing.T) {
	r := New(WithMaxInstances(2), WithMaxPerDef(2))
	r.RegisterDef(&Def{Name: "Q", Params: []Param{{Name: "T"}}})
	mustReq(t, r, "u1", "Q", []Type{NewNominal("int")}, nil)
	mustReq(t, r, "u1", "Q", []Type{NewNominal("bool")}, nil)
	if _, e := r.Request("u1", "Q", []Type{NewNominal("byte")}, nil); errCode(e) != ErrQuota {
		t.Fatalf("new instance at equality must be rejected, got %v", e)
	}
	before := r.SnapshotView()
	mustReq(t, r, "u2", "Q", []Type{NewNominal("int")}, nil)
	after := r.SnapshotView()
	if after.ValidCount != before.ValidCount || after.TotalHits != before.TotalHits+1 {
		t.Fatalf("hit must not consume quota")
	}

	r2 := New(WithMaxInstances(10), WithMaxPerDef(1))
	r2.RegisterDef(&Def{Name: "Q", Params: []Param{{Name: "T"}}})
	mustReq(t, r2, "u1", "Q", []Type{NewNominal("int")}, nil)
	if _, e := r2.Request("u1", "Q", []Type{NewNominal("bool")}, nil); errCode(e) != ErrQuota {
		t.Fatalf("per-def quota at equality must reject, got %v", e)
	}
}

func TestStaleStillConsumesQuota(t *testing.T) {
	r := New(WithMaxInstances(1))
	r.RegisterDef(&Def{Name: "Q", Params: []Param{{Name: "T"}}})
	mustReq(t, r, "u1", "Q", []Type{NewNominal("int")}, nil)
	r.UpdateDef(&Def{Name: "Q", Params: []Param{{Name: "T"}}})
	if _, e := r.Request("u2", "Q", []Type{NewNominal("int")}, nil); errCode(e) != ErrQuota {
		t.Fatalf("stale instance must still hold quota before cleanup, got %v", e)
	}
	old := -1
	for id := range r.instances {
		old = id
	}
	if e := r.Cleanup(old); e != nil {
		t.Fatalf("cleanup should succeed: %v", e)
	}
	mustReq(t, r, "u3", "Q", []Type{NewNominal("int")}, nil)
}

// ---------- 6. 并发：等价请求只建一份；请求与更新并发时实例自洽 ----------

func TestConcurrentEquivalentRequests(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "Box", Params: []Param{{Name: "T"}}})
	const n = 64
	var wg sync.WaitGroup
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			var arg Type = NewNominal("int")
			if i%2 == 0 {
				arg = NewAlias("alias-int", NewNominal("int"))
			}
			in, e := r.Request("unit-"+itoa(i), "Box", []Type{arg}, nil)
			if e != nil {
				t.Errorf("concurrent request rejected: %v", e)
				return
			}
			results[i] = in.ID
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent equivalent requests diverged: %d vs %d", results[0], results[i])
		}
	}
	snap := r.SnapshotView()
	if snap.ValidCount != 1 || snap.TotalCreates != 1 || snap.TotalHits != n-1 {
		t.Fatalf("want 1 create / %d hits, got %+v", n-1, snap)
	}
}

func TestConcurrentRequestVersusUpdate(t *testing.T) {
	r := New(WithMaxDepth(8))
	r.RegisterDef(&Def{Name: "A", Params: []Param{{Name: "T"}}})
	r.RegisterDef(&Def{Name: "B", Params: []Param{{Name: "T"}}})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			r.UpdateDef(&Def{Name: "A", Params: []Param{{Name: "T"}}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			in, e := r.Request("u", "B", []Type{NewNominal("int")}, func(s *Session) error {
				_, e2 := s.Request("A", []Type{NewNominal("int")}, nil)
				return e2
			})
			if e != nil {
				t.Errorf("request failed: %v", e)
				return
			}
			cur := r.Lookup(in.ID)
			if cur == nil {
				t.Errorf("returned instance vanished")
			}
		}
	}()
	wg.Wait()
}

// ---------- 7. 复杂度可验证 ----------

func TestHitLookupConstantTime(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "K", Params: []Param{{Name: "T"}}})
	const many = 500
	for i := 0; i < many; i++ {
		mustReq(t, r, "u1", "K", []Type{NewNominal("t" + itoa(i))}, nil)
	}
	r.lookupSteps = 0
	mustReq(t, r, "u2", "K", []Type{NewNominal("t0")}, nil)
	stepsSmall := r.lookupSteps
	r.lookupSteps = 0
	mustReq(t, r, "u2", "K", []Type{NewNominal("t" + itoa(many-1))}, nil)
	stepsLarge := r.lookupSteps
	if stepsSmall != 1 || stepsLarge != 1 {
		t.Fatalf("each hit must cost exactly one index probe, got %d/%d", stepsSmall, stepsLarge)
	}
}

func TestStalePropagationVisitsOnlyAffected(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "Hot", Params: []Param{{Name: "T"}}})
	r.RegisterDef(&Def{Name: "Cold", Params: []Param{{Name: "T"}}})
	for i := 0; i < 300; i++ {
		mustReq(t, r, "c", "Cold", []Type{NewNominal("c" + itoa(i))}, nil)
	}
	hot1 := mustReq(t, r, "h", "Hot", []Type{NewNominal("int")}, nil)
	hot2 := mustReq(t, r, "h", "Hot", []Type{NewNominal("bool")}, nil)
	r.UpdateDef(&Def{Name: "Hot", Params: []Param{{Name: "T"}}})
	if r.staleVisits != 2 {
		t.Fatalf("stale propagation must visit only affected set (2), got %d", r.staleVisits)
	}
	if r.Lookup(hot1.ID).Stale() == nil || r.Lookup(hot2.ID).Stale() == nil {
		t.Fatal("hot instances must be stale")
	}
}

// ---------- 8. 汇总视图与日志 ----------

func TestSnapshotAndLog(t *testing.T) {
	r := New()
	r.RegisterDef(&Def{Name: "V", Params: []Param{{Name: "T"}}})
	mustReq(t, r, "u1", "V", []Type{NewNominal("int")}, nil)
	mustReq(t, r, "u2", "V", []Type{NewNominal("int")}, nil)
	snap := r.SnapshotView()
	if snap.ValidCount != 1 || snap.StaleCount != 0 || snap.PerDefValid["V"] != 1 ||
		snap.TotalHits != 1 || snap.TotalCreates != 1 || snap.TakenAt.IsZero() {
		t.Fatalf("inconsistent snapshot: %+v", snap)
	}

	logger := NewTestLogger()
	rl := New(WithLogger(logger))
	rl.RegisterDef(&Def{Name: "W", Params: []Param{{Name: "T"}}})
	_, _ = rl.Request("u9", "W", []Type{NewNominal("int")}, nil)
	_, _ = rl.Request("u9", "W", []Type{NewNominal("int")}, nil)
	_, _ = rl.Request("u9", "Missing", nil, nil)
	var dump strings.Builder
	foundCreate, foundHit, foundReject := false, false, false
	for _, line := range logger.Lines {
		dump.WriteString(line + "\n")
		if strings.Contains(line, "create") {
			foundCreate = true
		}
		if strings.Contains(line, "hit") {
			foundHit = true
		}
		if strings.Contains(line, "code=undefined") {
			foundReject = true
		}
	}
	if !(foundCreate && foundHit && foundReject) {
		t.Fatalf("log must show input/output/reason:\n%s", dump.String())
	}
	t.Logf("captured log:\n%s", dump.String())
}

// ---------- 2. 嵌套实例化、深度上限与整体撤销 ----------

func linkBodyAt(chain []string, idx int) Body {
	if idx == len(chain)-1 {
		return nil
	}
	return func(s *Session) error {
		_, e := s.Request(chain[idx+1], []Type{NewNominal("int")}, linkBodyAt(chain, idx+1))
		return e
	}
}

func TestDepthLimitRollsBackWholeTree(t *testing.T) {
	r := New(WithMaxDepth(3))
	for _, n := range []string{"L0", "L1", "L2", "L3", "L4"} {
		r.RegisterDef(&Def{Name: n, Params: []Param{{Name: "T"}}})
	}

	// 先建立一个会被深层请求复用的已有实例 L4[int]。
	r.RegisterDef(&Def{Name: "Pre", Params: []Param{{Name: "T"}}})
	pre := mustReq(t, r, "u1", "Pre", []Type{NewNominal("int")},
		func(s *Session) error {
			in, e := s.Request("L4", []Type{NewNominal("int")}, nil)
			if e != nil {
				return e
			}
			_ = in
			return nil
		})
	_ = pre

	before := r.SnapshotView().ValidCount

	// L0->L1->L2->L3->L4 在第 4 层触发深度错误（上限 3）。
	chain := []string{"L0", "L1", "L2", "L3", "L4"}
	_, e := r.Request("u2", "L0", []Type{NewNominal("int")}, linkBodyAt(chain, 0))
	if errCode(e) != ErrDepth {
		t.Fatalf("want depth error, got %v", e)
	}

	// 整棵半成品实例树撤销：有效实例数与被复用实例完好无损。
	after := r.SnapshotView()
	if after.ValidCount != before {
		t.Fatalf("rollback must remove all intermediates: before=%d after=%d", before, after.ValidCount)
	}
	if after.TotalCreates != before {
		t.Fatalf("creates counter must be undone, got %d want %d", after.TotalCreates, before)
	}
	// L4[int] 是失败前已存在且被复用的实例，必须仍可命中。
	h1 := mustReq(t, r, "u3", "L4", []Type{NewNominal("int")}, nil)
	h2 := mustReq(t, r, "u4", "L4", []Type{NewNominal("int")}, nil)
	if h1.ID != h2.ID {
		t.Fatalf("reused instance must survive rollback")
	}

	// 同定义以自身实例为实参允许；深度足够时成功且只形成合法依赖。
	okReg := New(WithMaxDepth(2))
	okReg.RegisterDef(&Def{Name: "Self", Params: []Param{{Name: "T"}}})
	var selfInst *Instance
	root := mustReq(t, okReg, "u1", "Self", []Type{NewNominal("int")}, func(s *Session) error {
		in, e := s.Request("Self", []Type{NewInstanceRef("Self", []Type{NewNominal("int")})}, nil)
		if e != nil {
			return e
		}
		selfInst = in
		return nil
	})
	if len(root.Deps()) != 1 || root.Deps()[0] != selfInst.ID {
		t.Fatalf("want root -> self-instance edge, got %v", root.Deps())
	}
}

// ---------- 3. 更新定义：多层过期传导与清理条件 ----------

func TestStalePropagationAndCleanup(t *testing.T) {
	r := New(WithMaxDepth(8))
	for _, n := range []string{"A", "B", "C"} {
		r.RegisterDef(&Def{Name: n, Params: []Param{{Name: "T"}}})
	}
	r.RegisterDef(&Def{Name: "Z", Params: []Param{{Name: "T"}}})

	// 依赖链：C -> B -> A；Z 独立。
	rootC := mustReq(t, r, "u1", "C", []Type{NewNominal("int")}, func(s *Session) error {
		_, e := s.Request("B", []Type{NewNominal("int")}, func(s *Session) error {
			_, e2 := s.Request("A", []Type{NewNominal("int")}, nil)
			return e2
		})
		return e
	})
	z := mustReq(t, r, "u1", "Z", []Type{NewNominal("int")}, nil)

	r.UpdateDef(&Def{Name: "A", Params: []Param{{Name: "T"}}})

	byID := map[int]*Instance{}
	for _, in := range r.instances {
		byID[in.ID] = in
	}
	for _, in := range byID {
		if in.Def == "Z" {
			if in.stale != nil {
				t.Fatalf("unrelated Z must stay live")
			}
			continue
		}
		if in.stale == nil {
			t.Fatalf("%s instance must be stale after A update", in.Def)
		}
	}
	wantDepth := map[string]int{"A": 0, "B": 1, "C": 2}
	for _, in := range byID {
		if d, ok := wantDepth[in.Def]; ok && in.stale.Depth != d {
			t.Fatalf("%s stale depth want %d got %d", in.Def, d, in.stale.Depth)
		}
		if in.stale != nil && in.stale.ReasonDef != "A" {
			t.Fatalf("stale reason must be A")
		}
	}
	if r.SnapshotView().StaleCount != 3 {
		sn := r.SnapshotView()
		t.Fatalf("want 3 stale instances, got stale=%d valid=%d perStale=%v", sn.StaleCount, sn.ValidCount, sn.PerDefStale)
	}

	// 过期实例不可命中：再次请求 A[int] 得到全新实例（旧 A 仍可按 ID 查询到过期状态）。
	oldA := -1
	oldB := -1
	oldC := rootC.ID
	for _, in := range byID {
		switch in.Def {
		case "A":
			oldA = in.ID
		case "B":
			oldB = in.ID
		}
	}
	newA := mustReq(t, r, "u2", "A", []Type{NewNominal("int")}, nil)
	if newA.ID == oldA {
		t.Fatal("stale instance must not be re-hit")
	}
	if r.Lookup(oldA).Stale() == nil {
		t.Fatal("old A must remain queryable as stale until cleanup")
	}
	// C（过期）无未过期依赖者，可清理。
	if e := r.Cleanup(oldC); e != nil {
		t.Fatalf("cleanup C should succeed: %v", e)
	}
	if e := r.Cleanup(oldB); e != nil {
		t.Fatalf("cleanup B should succeed after C removed: %v", e)
	}
	if e := r.Cleanup(oldA); e != nil {
		t.Fatalf("cleanup A should succeed: %v", e)
	}
	// 未过期实例清理被拒。
	if e := r.Cleanup(z.ID); errCode(e) != ErrDependency {
		t.Fatalf("cleaning live instance must be dependency error, got %v", e)
	}
}
