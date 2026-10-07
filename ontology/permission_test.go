package ontology

import "testing"

func roleWorld(t *testing.T) *Arbiter {
	t.Helper()
	a := NewArbiter()
	must(t, a, RegisterObjectType{Name: "Person", Attrs: []AttrSpec{
		{Name: "employer", Default: Visible},
	}})
	must(t, a, CreateInstance{ID: "p1", TypeName: "Person"})
	for _, r := range []string{"R1", "R2", "R3"} {
		must(t, a, AddRole{Role: r})
	}
	// 层级：R1 -> R2 -> R3（包含方向）。
	must(t, a, IncludeRole{Child: "R1", Parent: "R2"})
	must(t, a, IncludeRole{Child: "R2", Parent: "R3"})
	must(t, a, AssignRole{Operator: "alice", Role: "R1"})
	return a
}

// TestNearestOverrideWins 沿层级 R1(1)->R2(2)->R3(3)，远端收紧、近端放宽时取近端。
func TestNearestOverrideWins(t *testing.T) {
	a := roleWorld(t)
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "R3", Vis: Invisible})
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "R1", Vis: Visible})

	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  解析 alice 对 p1.employer（R1=VISIBLE@dist1, R3=INVISIBLE@dist3）")
	echo(t, "OUTPUT %s", basisString(b))
	echo(t, "BASIS  最近距离=%d 命中=%s", b.Distance, b.Source)
	if b.Vis != Visible || b.Distance != 1 || b.Source != "ROLE:R1" {
		t.Fatalf("nearest override (R1@1) must win, got %s", basisString(b))
	}

	// 收紧的最近端改为不可见：只删不了声明，改为重声明 R1 为不可见并更新声明时间。
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "R1", Vis: Invisible})
	b2 := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  R1 重声明为 INVISIBLE（同主体更新，仍在 dist1）")
	echo(t, "OUTPUT %s", basisString(b2))
	if b2.Vis != Invisible || b2.Distance != 1 {
		t.Fatalf("re-declared R1 override must flip result, got %s", basisString(b2))
	}
}

// TestFarRoleReachable 只有远端 R3 声明时，必须沿 3 层找到它。
func TestFarRoleReachable(t *testing.T) {
	a := roleWorld(t)
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "R3", Vis: Invisible})
	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  仅 R3@dist3 声明 INVISIBLE")
	echo(t, "OUTPUT %s", basisString(b))
	if b.Vis != Invisible || b.Distance != 3 || b.Source != "ROLE:R3" {
		t.Fatalf("far override at dist 3 must be reached, got %s", basisString(b))
	}
}

// TestSameDistanceLatestWins 同层级距离时取声明时间更晚的覆盖。
func TestSameDistanceLatestWins(t *testing.T) {
	a := NewArbiter()
	must(t, a, RegisterObjectType{Name: "Person", Attrs: []AttrSpec{{Name: "employer", Default: Visible}}})
	must(t, a, CreateInstance{ID: "p1", TypeName: "Person"})
	must(t, a, AddRole{Role: "A"})
	must(t, a, AddRole{Role: "B"})
	must(t, a, AssignRole{Operator: "alice", Role: "A"})
	must(t, a, AssignRole{Operator: "alice", Role: "B"})

	// A、B 都在距离 1。先声明 A=INVISIBLE，稍后声明 B=VISIBLE，应取 B。
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "A", Vis: Invisible})
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "B", Vis: Visible})
	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  同距离：A=INVISIBLE 先声明，B=VISIBLE 后声明")
	echo(t, "OUTPUT %s", basisString(b))
	if b.Vis != Visible || b.Source != "ROLE:B" || b.Distance != 1 {
		t.Fatalf("later same-distance declaration (B) must win, got %s", basisString(b))
	}

	// 再给 A 发一条新声明（仍是距离 1，但时间更晚），应翻转回 A 的值。
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "A", Vis: Invisible})
	b2 := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  A 再次声明 INVISIBLE（同距离但时间最新）")
	echo(t, "OUTPUT %s", basisString(b2))
	if b2.Vis != Invisible || b2.Source != "ROLE:A" {
		t.Fatalf("fresh A declaration must win, got %s", basisString(b2))
	}
}

// TestDirectOverrideAndIsolation 距离 0 的操作者直接声明压制一切角色覆盖；
// 且该覆盖只影响该操作者本人。
func TestDirectOverrideAndIsolation(t *testing.T) {
	a := roleWorld(t)
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "R1", Vis: Invisible})
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectOperator, Subject: "alice", Vis: Visible})

	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  alice 直接 VISIBLE@0，R1 INVISIBLE@1")
	echo(t, "OUTPUT alice=%s", basisString(b))
	if b.Vis != Visible || b.Distance != 0 {
		t.Fatalf("direct override at dist 0 must win, got %s", basisString(b))
	}

	// 同属 R1 的 bob 没有自己的直接覆盖，仍沿角色得到 INVISIBLE。
	must(t, a, AssignRole{Operator: "bob", Role: "R1"})
	bb := a.Visibility("bob", "p1", "employer")
	echo(t, "OUTPUT bob=%s（验证操作者覆盖彼此隔离）", basisString(bb))
	if bb.Vis != Invisible {
		t.Fatalf("alice's direct override must not leak to bob, got %s", basisString(bb))
	}
}

// TestFallbackToTypeDefault 无任何可达覆盖时退回类型层默认；默认被修改后跟随变化。
func TestFallbackToTypeDefault(t *testing.T) {
	a := roleWorld(t)
	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  无任何覆盖")
	echo(t, "OUTPUT %s", basisString(b))
	if b.Vis != Visible || b.Distance != -1 || b.Source != "TYPE_DEFAULT" {
		t.Fatalf("should fall back to type default, got %s", basisString(b))
	}
	must(t, a, SetTypeDefault{TypeName: "Person", Attr: "employer", Vis: Invisible})
	b2 := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  类型默认改为 INVISIBLE")
	echo(t, "OUTPUT %s", basisString(b2))
	if b2.Vis != Invisible || b2.Distance != -1 {
		t.Fatalf("updated type default must apply, got %s", basisString(b2))
	}
}

// TestRoleCycleSafe 角色层级成环时解析必须终止且结果正确。
func TestRoleCycleSafe(t *testing.T) {
	a := NewArbiter()
	must(t, a, RegisterObjectType{Name: "Person", Attrs: []AttrSpec{{Name: "employer", Default: Visible}}})
	must(t, a, CreateInstance{ID: "p1", TypeName: "Person"})
	must(t, a, AddRole{Role: "X"})
	must(t, a, AddRole{Role: "Y"})
	must(t, a, IncludeRole{Child: "X", Parent: "Y"})
	must(t, a, IncludeRole{Child: "Y", Parent: "X"})
	must(t, a, AssignRole{Operator: "alice", Role: "X"})
	must(t, a, DeclareOverride{InstanceID: "p1", Attr: "employer", Kind: SubjectRole, Subject: "Y", Vis: Invisible})
	// X 与 Y 互为包含，无环处理下应在有限步内终止并命中 dist=2(X->Y) 的声明。
	b := a.Visibility("alice", "p1", "employer")
	echo(t, "INPUT  环形层级 X<->Y，Y@环上声明 INVISIBLE")
	echo(t, "OUTPUT %s", basisString(b))
	if b.Vis != Invisible || b.Distance != 2 {
		t.Fatalf("cycle must be handled with shortest distance 2, got %s", basisString(b))
	}
}
