package ontology

import (
	"errors"
	"testing"
)

// twoNodeEnv 构造 A --L--> B 的双节点图环境，便于聚焦覆盖规则。
type twoNodeEnv struct {
	reg     *PermissionRegistry
	service *Service
}

func newTwoNodeEnv(t *testing.T, policy ConflictPolicy) *twoNodeEnv {
	t.Helper()
	reg := NewPermissionRegistry(policy)
	g := NewGraph()
	g.AddObject("A", "TA")
	g.AddObject("B", "TB")
	if err := g.AddLink(Link{From: "A", To: "B", Type: "L", Cost: 1}); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	return &twoNodeEnv{reg: reg, service: NewService(reg, g)}
}

// queryAB 以主体 p 查询 A 到 B 的路径状态。
func (e *twoNodeEnv) queryAB(t *testing.T, p PrincipalID) QueryResult {
	t.Helper()
	res, err := e.service.ShortestPath(p, "A", "B")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}
	return res
}

func TestLinkLayerOverridesObjectLayer(t *testing.T) {
	cases := []struct {
		name       string
		objectDecl DeclValue
		linkDecl   DeclValue // 0 表示未声明
		want       QueryStatus
	}{
		{"链接允许覆盖对象拒绝", DeclDeny, DeclAllow, StatusReachable},
		{"链接拒绝覆盖对象允许", DeclAllow, DeclDeny, StatusUnreachable},
		{"链接允许且对象未声明", 0, DeclAllow, StatusReachable},
		{"链接拒绝且对象未声明", 0, DeclDeny, StatusUnreachable},
		{"链接未声明回退对象允许", DeclAllow, 0, StatusReachable},
		{"链接未声明回退对象拒绝", DeclDeny, 0, StatusUnreachable},
		{"两层均未声明默认拒绝", 0, 0, StatusUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTwoNodeEnv(t, DenyOverrides)
			env.reg.UpsertGroup("g1", 1)
			env.reg.AssignPrincipal("p1", "g1")
			if tc.objectDecl != 0 {
				env.reg.SetObjectTypeDeclaration("g1", "TA", tc.objectDecl)
				env.reg.SetObjectTypeDeclaration("g1", "TB", tc.objectDecl)
			}
			if tc.linkDecl != 0 {
				env.reg.SetLinkTypeDeclaration("g1", "L", tc.linkDecl)
			}
			if got := env.queryAB(t, "p1").Status; got != tc.want {
				t.Fatalf("status = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHighestPriorityGroupWins(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("low", 1)
	env.reg.UpsertGroup("high", 9)
	env.reg.AssignPrincipal("p1", "low")
	env.reg.AssignPrincipal("p1", "high")
	env.reg.SetLinkTypeDeclaration("low", "L", DeclDeny)
	env.reg.SetLinkTypeDeclaration("high", "L", DeclAllow)

	res := env.queryAB(t, "p1")
	if res.Status != StatusReachable {
		t.Fatalf("status = %v, want reachable", res.Status)
	}
	if len(res.Trace) != 1 {
		t.Fatalf("trace len = %d, want 1", len(res.Trace))
	}
	basis := res.Trace[0].Bases[0]
	if basis.Layer != LayerLink || basis.Priority != 9 {
		t.Fatalf("basis = %+v, want link layer at priority 9", basis)
	}
	if len(basis.Groups) != 1 || basis.Groups[0] != "high" {
		t.Fatalf("basis groups = %v, want [high]", basis.Groups)
	}
}

func TestSamePriorityConflictDenyWins(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("g1", 5)
	env.reg.UpsertGroup("g2", 5)
	env.reg.AssignPrincipal("p1", "g1")
	env.reg.AssignPrincipal("p1", "g2")
	env.reg.SetLinkTypeDeclaration("g1", "L", DeclAllow)
	env.reg.SetLinkTypeDeclaration("g2", "L", DeclDeny)

	res := env.queryAB(t, "p1")
	if res.Status != StatusUnreachable {
		t.Fatalf("status = %v, want unreachable (deny wins)", res.Status)
	}
	basis := res.Trace[0].Bases[0]
	if basis.Merged != DecisionDeny {
		t.Fatalf("merged = %v, want deny", basis.Merged)
	}
	if len(basis.Groups) != 2 {
		t.Fatalf("basis groups = %v, want both groups recorded", basis.Groups)
	}
}

func TestSamePriorityConflictStrictAmbiguous(t *testing.T) {
	env := newTwoNodeEnv(t, StrictConflict)
	env.reg.UpsertGroup("g1", 5)
	env.reg.UpsertGroup("g2", 5)
	env.reg.AssignPrincipal("p1", "g1")
	env.reg.AssignPrincipal("p1", "g2")
	env.reg.SetLinkTypeDeclaration("g1", "L", DeclAllow)
	env.reg.SetLinkTypeDeclaration("g2", "L", DeclDeny)

	res := env.queryAB(t, "p1")
	if res.Status != StatusAmbiguous {
		t.Fatalf("status = %v, want ambiguous", res.Status)
	}
	if res.Trace[0].Decision != DecisionAmbiguous {
		t.Fatalf("decision = %v, want ambiguous", res.Trace[0].Decision)
	}
}

func TestUnreachableDistinctFromAmbiguous(t *testing.T) {
	// 同样的矛盾声明：默认策略下合并为拒绝（不可达），严格策略下为歧义。
	build := func(policy ConflictPolicy) *twoNodeEnv {
		env := newTwoNodeEnv(t, policy)
		env.reg.UpsertGroup("g1", 5)
		env.reg.UpsertGroup("g2", 5)
		env.reg.AssignPrincipal("p1", "g1")
		env.reg.AssignPrincipal("p1", "g2")
		env.reg.SetLinkTypeDeclaration("g1", "L", DeclAllow)
		env.reg.SetLinkTypeDeclaration("g2", "L", DeclDeny)
		return env
	}
	if got := build(DenyOverrides).queryAB(t, "p1").Status; got != StatusUnreachable {
		t.Fatalf("DenyOverrides status = %v, want unreachable", got)
	}
	if got := build(StrictConflict).queryAB(t, "p1").Status; got != StatusAmbiguous {
		t.Fatalf("StrictConflict status = %v, want ambiguous", got)
	}
}

func TestObjectLayerMergesBothEndpointsDenyFirst(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("g1", 1)
	env.reg.AssignPrincipal("p1", "g1")
	env.reg.SetObjectTypeDeclaration("g1", "TA", DeclAllow)
	env.reg.SetObjectTypeDeclaration("g1", "TB", DeclDeny)

	res := env.queryAB(t, "p1")
	if res.Status != StatusUnreachable {
		t.Fatalf("status = %v, want unreachable (endpoint deny wins)", res.Status)
	}
	if len(res.Trace[0].Bases) != 2 {
		t.Fatalf("bases = %+v, want two endpoint bases", res.Trace[0].Bases)
	}
}

func TestObjectLayerSingleEndpointDeclared(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("g1", 1)
	env.reg.AssignPrincipal("p1", "g1")
	env.reg.SetObjectTypeDeclaration("g1", "TB", DeclAllow)

	if got := env.queryAB(t, "p1").Status; got != StatusReachable {
		t.Fatalf("status = %v, want reachable", got)
	}
}

func TestUndeclaredGroupDoesNotParticipate(t *testing.T) {
	// 高优先级组未声明该目标时，应回退到低优先级组的声明。
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("silent", 9)
	env.reg.UpsertGroup("low", 1)
	env.reg.AssignPrincipal("p1", "silent")
	env.reg.AssignPrincipal("p1", "low")
	env.reg.SetLinkTypeDeclaration("low", "L", DeclAllow)

	res := env.queryAB(t, "p1")
	if res.Status != StatusReachable {
		t.Fatalf("status = %v, want reachable", res.Status)
	}
	if basis := res.Trace[0].Bases[0]; basis.Priority != 1 {
		t.Fatalf("basis priority = %d, want 1", basis.Priority)
	}
}

func TestInvalidPrincipalRejected(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	for _, bad := range []PrincipalID{"", "bad id", "bad/id", "bad\nid"} {
		if _, err := env.service.ShortestPath(bad, "A", "B"); !errors.Is(err, ErrInvalidPrincipal) {
			t.Fatalf("principal %q: err = %v, want ErrInvalidPrincipal", bad, err)
		}
	}
}

func TestUnknownObjectRejected(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	if _, err := env.service.ShortestPath("p1", "A", "ZZ"); !errors.Is(err, ErrUnknownObject) {
		t.Fatalf("err = %v, want ErrUnknownObject", err)
	}
}

func TestDeclarationChangeTakesEffectOnNextQuery(t *testing.T) {
	env := newTwoNodeEnv(t, DenyOverrides)
	env.reg.UpsertGroup("g1", 1)
	env.reg.AssignPrincipal("p1", "g1")
	env.reg.SetLinkTypeDeclaration("g1", "L", DeclAllow)
	if got := env.queryAB(t, "p1").Status; got != StatusReachable {
		t.Fatalf("before change: status = %v, want reachable", got)
	}
	env.reg.SetLinkTypeDeclaration("g1", "L", DeclDeny)
	if got := env.queryAB(t, "p1").Status; got != StatusUnreachable {
		t.Fatalf("after change: status = %v, want unreachable", got)
	}
	env.reg.ClearLinkTypeDeclaration("g1", "L")
	env.reg.SetObjectTypeDeclaration("g1", "TA", DeclAllow)
	if got := env.queryAB(t, "p1").Status; got != StatusReachable {
		t.Fatalf("after clear: status = %v, want reachable via object layer", got)
	}
}
