package ontology

import (
	"fmt"
	"slices"
	"testing"
)

const testCaller = Caller("alice")

type rolePri struct {
	role Role
	pri  int
}

func rp(role Role, pri int) rolePri { return rolePri{role: role, pri: pri} }

type fixtureBuilder struct {
	t *testing.T
	s *Store
}

func newFixture(t *testing.T) *fixtureBuilder {
	t.Helper()
	return &fixtureBuilder{t: t, s: NewStore()}
}

func (f *fixtureBuilder) obj(ids ...ObjectID) *fixtureBuilder {
	f.t.Helper()
	for _, id := range ids {
		f.s.AddObject(id)
		f.s.GrantExistence(testCaller, id)
	}
	return f
}

func (f *fixtureBuilder) ltype(name LinkTypeName, cost int64, roles ...rolePri) *fixtureBuilder {
	f.t.Helper()
	f.s.AddLinkType(name, cost)
	f.s.GrantTraversal(testCaller, name)
	for _, r := range roles {
		if err := f.s.DeclareRole(name, r.role, r.pri); err != nil {
			f.t.Fatalf("DeclareRole(%s,%s): %v", name, r.role, err)
		}
	}
	return f
}

func (f *fixtureBuilder) link(typ LinkTypeName, from, to ObjectID) *fixtureBuilder {
	f.t.Helper()
	if err := f.s.AddLink(typ, from, to); err != nil {
		f.t.Fatalf("AddLink(%s,%s,%s): %v", typ, from, to, err)
	}
	return f
}

func (f *fixtureBuilder) store() *Store { return f.s }

func stepTypes(res Result) []LinkTypeName {
	out := make([]LinkTypeName, 0, len(res.Steps))
	for _, st := range res.Steps {
		out = append(out, st.LinkType)
	}
	return out
}

func stepObjs(res Result) []ObjectID {
	if len(res.Steps) == 0 {
		return nil
	}
	out := make([]ObjectID, 0, len(res.Steps)+1)
	out = append(out, res.Steps[0].From)
	for _, st := range res.Steps {
		out = append(out, st.To)
	}
	return out
}

// twoBranch 构造 A 经 T1/T2 分别到 B/C、再分别经 T3/T4 到 D 的两分支图。
// T1、T2 均承担角色 "r"（优先级为 pri1、pri2），T3、T4 承担角色 "r2"。
func twoBranch(t *testing.T, pri1, pri2 int) *Store {
	t.Helper()
	return newFixture(t).
		obj("A", "B", "C", "D").
		ltype("T1", 1, rp("r", pri1)).
		ltype("T2", 1, rp("r", pri2)).
		ltype("T3", 1, rp("r2", 1)).
		ltype("T4", 1, rp("r2", 1)).
		link("T1", "A", "B").
		link("T2", "A", "C").
		link("T3", "B", "D").
		link("T4", "C", "D").
		store()
}

var twoBranchQuery = Query{Caller: testCaller, Start: "A", End: "D", Roles: []Role{"r", "r2"}}

func TestUniqueByPriority(t *testing.T) {
	s := twoBranch(t, 1, 2)
	res := s.Query(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if got, want := stepTypes(res), []LinkTypeName{"T1", "T3"}; !slices.Equal(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	if got, want := stepObjs(res), []ObjectID{"A", "B", "D"}; !slices.Equal(got, want) {
		t.Fatalf("objs = %v, want %v", got, want)
	}
	if res.TotalCost != 2 {
		t.Fatalf("cost = %d, want 2", res.TotalCost)
	}

	// 调整优先级后结果随之翻转。
	if err := s.SetRolePriority("T2", "r", 0); err != nil {
		t.Fatal(err)
	}
	res = s.Query(twoBranchQuery)
	if got, want := stepTypes(res), []LinkTypeName{"T2", "T4"}; !slices.Equal(got, want) {
		t.Fatalf("after priority change, types = %v, want %v", got, want)
	}
}

func TestAmbiguousTie(t *testing.T) {
	s := twoBranch(t, 1, 1)
	res := s.Query(twoBranchQuery)
	if res.Status != StatusAmbiguous {
		t.Fatalf("status = %v, want Ambiguous", res.Status)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("steps = %v, want empty", res.Steps)
	}
	if len(res.Ambiguous) != 1 {
		t.Fatalf("ambiguous steps = %v, want exactly 1", res.Ambiguous)
	}
	amb := res.Ambiguous[0]
	if amb.Index != 0 || amb.At != "A" || amb.Role != "r" || amb.Priority != 1 {
		t.Fatalf("ambiguous step = %+v, want {Index:0 At:A Role:r Priority:1}", amb)
	}
	if want := []LinkTypeName{"T1", "T2"}; !slices.Equal(amb.Candidates, want) {
		t.Fatalf("candidates = %v, want %v", amb.Candidates, want)
	}
}

func TestPermissionResolvesAmbiguity(t *testing.T) {
	s := twoBranch(t, 1, 1)

	// 收回 T2 的遍历权限后，候选只剩 T1，歧义被消解。
	s.RevokeTraversal(testCaller, "T2")
	res := s.Query(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if got, want := stepTypes(res), []LinkTypeName{"T1", "T3"}; !slices.Equal(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}

	// 对称地，只留 T2 时走 T2。
	s.GrantTraversal(testCaller, "T2")
	s.RevokeTraversal(testCaller, "T1")
	res = s.Query(twoBranchQuery)
	if got, want := stepTypes(res), []LinkTypeName{"T2", "T4"}; !slices.Equal(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
}

func TestUnreachableVsAmbiguous(t *testing.T) {
	// 不可达：角色均已声明，但没有任何链接实例。
	s := newFixture(t).
		obj("A", "D").
		ltype("T1", 1, rp("r", 1)).
		ltype("T3", 1, rp("r2", 1)).
		store()
	res := s.Query(twoBranchQuery)
	if res.Status != StatusUnreachable {
		t.Fatalf("status = %v, want Unreachable", res.Status)
	}
	if len(res.Ambiguous) != 0 {
		t.Fatalf("ambiguous = %v, want empty", res.Ambiguous)
	}

	// 歧义无法确定：存在候选路径，但第一步无法消解。
	s2 := twoBranch(t, 1, 1)
	res2 := s2.Query(twoBranchQuery)
	if res2.Status != StatusAmbiguous {
		t.Fatalf("status = %v, want Ambiguous", res2.Status)
	}
	if res.Status == res2.Status {
		t.Fatal("Unreachable 与 Ambiguous 必须严格区分")
	}
}

// 歧义步与干净路径并存时，只要存在不经过歧义步的路径即返回唯一结果。
func TestCleanPathCoexistsWithAmbiguousStep(t *testing.T) {
	s := newFixture(t).
		obj("A", "B", "C", "D").
		ltype("T1", 1, rp("r", 1)).
		ltype("T4", 1, rp("r2", 1)).
		ltype("T5", 1, rp("r2", 1)).
		ltype("T6", 1, rp("r2", 1)).
		link("T1", "A", "B").
		link("T1", "A", "C").
		link("T4", "B", "D").
		link("T5", "B", "D").
		link("T6", "C", "D").
		store()
	res := s.Query(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if got, want := stepObjs(res), []ObjectID{"A", "C", "D"}; !slices.Equal(got, want) {
		t.Fatalf("objs = %v, want %v", got, want)
	}
	// B 处的歧义步仍被记录，但不影响唯一结果的判定。
	if len(res.Ambiguous) != 1 || res.Ambiguous[0].At != "B" || res.Ambiguous[0].Index != 1 {
		t.Fatalf("ambiguous = %+v, want one step at B index 1", res.Ambiguous)
	}
}

func TestCostAndLexTieBreak(t *testing.T) {
	build := func(costB, costC int64) *Store {
		return newFixture(t).
			obj("A", "B", "C", "D").
			ltype("T1", 1, rp("r", 1)).
			ltype("T2", costB, rp("r2", 1)).
			ltype("T3", costC, rp("r2", 1)).
			link("T1", "A", "B").
			link("T1", "A", "C").
			link("T2", "B", "D").
			link("T3", "C", "D").
			store()
	}

	// 代价不同：选总代价最小（经 C，1+2=3 < 1+5=6）。
	res := build(5, 2).Query(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if got, want := stepObjs(res), []ObjectID{"A", "C", "D"}; !slices.Equal(got, want) {
		t.Fatalf("objs = %v, want %v", got, want)
	}
	if res.TotalCost != 3 {
		t.Fatalf("cost = %d, want 3", res.TotalCost)
	}

	// 代价相同：选对象标识序列字典序更小（[A,B,D] < [A,C,D]）。
	res = build(2, 2).Query(twoBranchQuery)
	if got, want := stepObjs(res), []ObjectID{"A", "B", "D"}; !slices.Equal(got, want) {
		t.Fatalf("objs = %v, want %v", got, want)
	}
}

func TestInvalidArgument(t *testing.T) {
	s := twoBranch(t, 1, 2)

	res := s.Query(Query{Caller: testCaller, Start: "A", End: "D"})
	if res.Status != StatusInvalidArgument {
		t.Fatalf("empty roles: status = %v, want InvalidArgument", res.Status)
	}

	res = s.Query(Query{Caller: testCaller, Start: "A", End: "D", Roles: []Role{"nope"}})
	if res.Status != StatusInvalidArgument {
		t.Fatalf("undeclared role: status = %v, want InvalidArgument", res.Status)
	}

	// 参数非法优先于存在性权限判定。
	res = s.Query(Query{Caller: "stranger", Start: "A", End: "D"})
	if res.Status != StatusInvalidArgument {
		t.Fatalf("invalid+denied: status = %v, want InvalidArgument", res.Status)
	}
}

func TestPermissionDenied(t *testing.T) {
	s := twoBranch(t, 1, 2)
	stranger := Caller("stranger")

	res := s.Query(Query{Caller: stranger, Start: "A", End: "D", Roles: []Role{"r", "r2"}})
	if res.Status != StatusPermissionDenied {
		t.Fatalf("status = %v, want PermissionDenied", res.Status)
	}

	// 只有起点有存在性权限、终点没有，同样拒绝。
	s.GrantExistence(stranger, "A")
	res = s.Query(Query{Caller: stranger, Start: "A", End: "D", Roles: []Role{"r", "r2"}})
	if res.Status != StatusPermissionDenied {
		t.Fatalf("status = %v, want PermissionDenied", res.Status)
	}

	// 存在性权限判定优先于不可达：无权限时不得泄露可达性。
	s.GrantExistence(stranger, "D")
	res = s.Query(Query{Caller: stranger, Start: "A", End: "D", Roles: []Role{"r", "r2"}})
	if res.Status != StatusUnreachable {
		t.Fatalf("status = %v, want Unreachable (stranger 无遍历权限)", res.Status)
	}
}

// 同一链接类型可同时承担多个角色。
func TestSameTypeMultipleRoles(t *testing.T) {
	s := newFixture(t).
		obj("A", "B", "D").
		ltype("T1", 2, rp("r", 1), rp("r2", 1)).
		link("T1", "A", "B").
		link("T1", "B", "D").
		store()
	res := s.Query(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if got, want := stepTypes(res), []LinkTypeName{"T1", "T1"}; !slices.Equal(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	if res.TotalCost != 4 {
		t.Fatalf("cost = %d, want 4", res.TotalCost)
	}
}

// 度量只统计本次查询实际展开的候选链接，与图总体规模无关。
func TestMetricIndependentOfGraphSize(t *testing.T) {
	s := twoBranch(t, 1, 2)
	_, m1 := s.queryWithMetrics(twoBranchQuery)
	if m1.ExpandedLinks == 0 {
		t.Fatal("metric should count expanded links")
	}

	// 加入大量与查询无关的对象与链接（含承担相同角色的链接类型）。
	s.AddLinkType("Noise", 1)
	if err := s.DeclareRole("Noise", "r", 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		id := ObjectID(fmt.Sprintf("N%d", i))
		s.AddObject(id)
		if i > 0 {
			prev := ObjectID(fmt.Sprintf("N%d", i-1))
			if err := s.AddLink("Noise", prev, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 从终点出发的链接同样不应被展开。
	if err := s.AddLink("Noise", "D", "N0"); err != nil {
		t.Fatal(err)
	}

	res, m2 := s.queryWithMetrics(twoBranchQuery)
	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK", res.Status)
	}
	if m2 != m1 {
		t.Fatalf("metric = %+v, want %+v (应与图总体规模无关)", m2, m1)
	}
}
