package ontology

import "testing"

// buildPerfWorld 构造：
//   - alice 沿主链 R0 -> R1 -> ... -> R(d-1)，最近覆盖在距离 d；
//   - 另有一个与 alice 完全不可达的独立巨型角色链 U0 -> U1 -> ...，
//     链上每个角色都声明同一属性的覆盖。
//
// 通过探针可以确定性地验证：解析只触及以 alice 为圆心、半径 d 的 BFS 球，
// 不可达的巨型组件一个节点都不会被访问。
func buildPerfWorld(t testing.TB, d, unrelated int) *Arbiter {
	t.Helper()
	a := NewArbiter()
	mustTB(t, a, RegisterObjectType{Name: "Person", Attrs: []AttrSpec{
		{Name: "employer", Default: Visible},
	}})
	mustTB(t, a, CreateInstance{ID: "p1", TypeName: "Person"})

	roleName := func(i int) string { return "R" + itoa(i) }
	unrelName := func(i int) string { return "U" + itoa(i) }

	for i := 0; i < d; i++ {
		mustTB(t, a, AddRole{Role: roleName(i)})
	}
	mustTB(t, a, AssignRole{Operator: "alice", Role: roleName(0)})
	for i := 0; i+1 < d; i++ {
		mustTB(t, a, IncludeRole{Child: roleName(i), Parent: roleName(i + 1)})
	}
	if d > 0 {
		mustTB(t, a, DeclareOverride{
			InstanceID: "p1", Attr: "employer",
			Kind: SubjectRole, Subject: roleName(d - 1), Vis: Invisible,
		})
	}

	for i := 0; i < unrelated; i++ {
		mustTB(t, a, AddRole{Role: unrelName(i)})
	}
	for i := 0; i+1 < unrelated; i++ {
		mustTB(t, a, IncludeRole{Child: unrelName(i), Parent: unrelName(i + 1)})
	}
	for i := 0; i < unrelated; i++ {
		mustTB(t, a, DeclareOverride{
			InstanceID: "p1", Attr: "employer",
			Kind: SubjectRole, Subject: unrelName(i), Vis: Visible,
		})
	}
	if unrelated > 0 {
		mustTB(t, a, AssignRole{Operator: "zoe", Role: unrelName(0)})
	}
	return a
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func mustTB(t testing.TB, a *Arbiter, cmd Command) {
	t.Helper()
	if err := a.Apply(cmd); err != nil {
		t.Fatalf("setup %#v failed: %v", cmd, err)
	}
}

// TestCostIndependentOfTotalRoles 固定最近覆盖距离 d=3，膨胀不可达角色规模，
// 探针访问的角色数必须恒为 3，检查数也不随规模增长。
func TestCostIndependentOfTotalRoles(t *testing.T) {
	sizes := []int{0, 10, 1000, 10000}
	var prev Probe
	for i, n := range sizes {
		a := buildPerfWorld(t, 3, n)
		basis, p := a.resolver.withProbe("alice", "p1", "employer", Visible, nil)
		echo(t, "INPUT  最近覆盖距离=3，系统角色总数=%d（含 %d 个不可达角色）", 3+n, n)
		echo(t, "OUTPUT %s", basisString(basis))
		echo(t, "BASIS  探针：访问角色=%d 检查声明=%d（应恒定，不随角色总数增长）",
			p.RolesVisited, p.OverridesChecked)
		if basis.Vis != Invisible || basis.Distance != 3 {
			t.Fatalf("must hit override at dist 3, got %s", basisString(basis))
		}
		if p.RolesVisited != 3 || p.OverridesChecked != 4 {
			t.Fatalf("probe must be constant {roles=3 checks=4}, got %+v", p)
		}
		if i > 0 && p != prev {
			t.Fatalf("probe counts changed as unrelated graph grew: %+v vs %+v", prev, p)
		}
		prev = p
	}
}

// TestCostScalesWithNearestDistance 最近覆盖距离变化时，访问角色数恰为距离值；
// 即使命中层之外还挂着 5000 个可经更远路径到达的角色，也一个都不会访问。
func TestCostScalesWithNearestDistance(t *testing.T) {
	for _, d := range []int{1, 2, 5, 10} {
		a := buildPerfWorld(t, d, 5000)
		mustTB(t, a, IncludeRole{Child: "R" + itoa(d-1), Parent: "U0"})
		basis, p := a.resolver.withProbe("alice", "p1", "employer", Visible, nil)
		echo(t, "INPUT  最近覆盖距离=%d，命中层之外另挂 5000 个可达角色", d)
		echo(t, "OUTPUT %s", basisString(basis))
		echo(t, "BASIS  探针：访问角色=%d（必须等于距离 %d，不进入更远层级）", p.RolesVisited, d)
		if basis.Distance != d || basis.Vis != Invisible {
			t.Fatalf("want hit at dist %d, got %s", d, basisString(basis))
		}
		if p.RolesVisited != d {
			t.Fatalf("roles visited must equal nearest distance %d, got %d", d, p.RolesVisited)
		}
	}

	a := buildPerfWorld(t, 5, 0)
	mustTB(t, a, DeclareOverride{
		InstanceID: "p1", Attr: "employer",
		Kind: SubjectOperator, Subject: "alice", Vis: Visible,
	})
	basis, p := a.resolver.withProbe("alice", "p1", "employer", Visible, nil)
	echo(t, "INPUT  操作者直接声明覆盖（距离 0）")
	echo(t, "OUTPUT %s", basisString(basis))
	echo(t, "BASIS  探针：访问角色=%d 检查声明=%d", p.RolesVisited, p.OverridesChecked)
	if basis.Distance != 0 || p.RolesVisited != 0 {
		t.Fatalf("distance 0 must touch no roles, got dist=%d visited=%d", basis.Distance, p.RolesVisited)
	}
}

// BenchmarkResolveHugeUnrelatedGraph 以墙钟时间旁证：10 万不可达角色存在时，
// 最近覆盖距离为 3 的解析仍只做常数级工作。
func BenchmarkResolveHugeUnrelatedGraph(b *testing.B) {
	a := buildPerfWorld(b, 3, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		basis, _ := a.resolver.withProbe("alice", "p1", "employer", Visible, nil)
		if basis.Distance != 3 {
			b.Fatalf("bad basis: %+v", basis)
		}
	}
}
