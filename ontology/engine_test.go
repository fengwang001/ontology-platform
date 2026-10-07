package ontology

import (
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// chainFixture builds types A,B,C with links ab: A->B and bc: B->C.
func chainFixture(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	for _, ot := range []string{"A", "B", "C"} {
		must(t, e.DeclareObjectType(ot))
	}
	must(t, e.DeclareLinkType("ab", "A", "B"))
	must(t, e.DeclareLinkType("bc", "B", "C"))
	must(t, e.DeclareTag("t"))
	return e
}

func tagsOf(t *testing.T, e *Engine, instance string) []string {
	t.Helper()
	tags, err := e.InstanceTags(instance)
	must(t, err)
	return tags
}

func TestMultiPathInheritanceConverges(t *testing.T) {
	e := chainFixture(t)
	// Diamond: D->B via db, so t reaches B both directly and through D.
	must(t, e.DeclareObjectType("D"))
	must(t, e.DeclareLinkType("db", "D", "B"))
	must(t, e.AttachTag("t", "A"))
	must(t, e.AttachTag("t", "D"))
	must(t, e.DeclarePropagation("t", "ab", Downstream))
	must(t, e.DeclarePropagation("t", "db", Downstream))
	must(t, e.DeclarePropagation("t", "bc", Downstream))
	must(t, e.DeclareInstance("inst-c", "C"))

	got := tagsOf(t, e, "inst-c")
	if !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("expected single converged tag, got %v", got)
	}

	// Rebuild the same declarations in a different order: same conclusion.
	e2 := NewEngine()
	for _, ot := range []string{"C", "B", "A", "D"} {
		must(t, e2.DeclareObjectType(ot))
	}
	must(t, e2.DeclareTag("t"))
	must(t, e2.DeclareLinkType("bc", "B", "C"))
	must(t, e2.DeclareLinkType("db", "D", "B"))
	must(t, e2.DeclareLinkType("ab", "A", "B"))
	must(t, e2.DeclarePropagation("t", "bc", Downstream))
	must(t, e2.DeclarePropagation("t", "db", Downstream))
	must(t, e2.DeclarePropagation("t", "ab", Downstream))
	must(t, e2.AttachTag("t", "D"))
	must(t, e2.AttachTag("t", "A"))
	must(t, e2.DeclareInstance("inst-c", "C"))
	if got2 := tagsOf(t, e2, "inst-c"); !reflect.DeepEqual(got, got2) {
		t.Fatalf("order changed result: %v vs %v", got, got2)
	}
}

func TestPropagationCycleTerminates(t *testing.T) {
	e := chainFixture(t)
	// Introduce a cycle C->A.
	must(t, e.DeclareLinkType("ca", "C", "A"))
	must(t, e.AttachTag("t", "A"))
	for _, l := range []string{"ab", "bc", "ca"} {
		must(t, e.DeclarePropagation("t", l, Downstream))
	}
	for _, inst := range []string{"ia", "ib", "ic"} {
		ot := map[string]string{"ia": "A", "ib": "B", "ic": "C"}[inst]
		must(t, e.DeclareInstance(inst, ot))
		if got := tagsOf(t, e, inst); !reflect.DeepEqual(got, []string{"t"}) {
			t.Fatalf("%s: expected [t], got %v", inst, got)
		}
	}
}

func TestUpstreamPropagation(t *testing.T) {
	e := chainFixture(t)
	must(t, e.AttachTag("t", "C"))
	must(t, e.DeclarePropagation("t", "bc", Upstream))
	must(t, e.DeclarePropagation("t", "ab", Upstream))
	must(t, e.DeclareInstance("ia", "A"))
	if got := tagsOf(t, e, "ia"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("expected upstream propagation to reach A, got %v", got)
	}
}

func TestBlockingPointStopsOnlyThroughPaths(t *testing.T) {
	e := chainFixture(t)
	// Second route into B: D->B.
	must(t, e.DeclareObjectType("D"))
	must(t, e.DeclareLinkType("db", "D", "B"))
	must(t, e.AttachTag("t", "A"))
	must(t, e.AttachTag("t", "D"))
	for _, l := range []string{"ab", "bc", "db"} {
		must(t, e.DeclarePropagation("t", l, Downstream))
	}
	must(t, e.DeclareInstance("ib", "B"))
	must(t, e.DeclareInstance("ic", "C"))

	// Block t across ab: the A->B->C path dies, the D->B->C path survives.
	must(t, e.DeclareBlock("t", "ab"))
	if got := tagsOf(t, e, "ic"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("alternate path must survive blocking, got %v", got)
	}

	// Block the remaining route: nothing reaches B or C any more.
	must(t, e.DeclareBlock("t", "db"))
	for _, inst := range []string{"ib", "ic"} {
		if got := tagsOf(t, e, inst); len(got) != 0 {
			t.Fatalf("%s: expected no tags, got %v", inst, got)
		}
	}

	// CheckTag reports the all-paths-blocked class.
	must(t, e.DeclareRole("r"))
	must(t, e.DeclareSubject("s", "r"))
	must(t, e.SetGrant("r", "t", Allow))
	_, err := e.CheckTag("s", "ic", "t")
	if ClassOf(err) != ClassAllPathsBlocked {
		t.Fatalf("expected ClassAllPathsBlocked, got %v", err)
	}

	// Removing one block reopens that path.
	must(t, e.RemoveBlock("t", "db"))
	if got := tagsOf(t, e, "ic"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("expected reopening, got %v", got)
	}
}

// grantFixture builds one instance of type A carrying tag t, plus roles
// child -> parent and a subject holding child.
func grantFixture(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	must(t, e.DeclareObjectType("A"))
	must(t, e.DeclareTag("t"))
	must(t, e.AttachTag("t", "A"))
	must(t, e.DeclareInstance("i", "A"))
	must(t, e.DeclareRole("parent"))
	must(t, e.DeclareRole("child", "parent"))
	must(t, e.DeclareSubject("s", "child"))
	return e
}

func authorize(t *testing.T, e *Engine) Decision {
	t.Helper()
	dec, err := e.Authorize("s", "i", "read")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return dec
}

func TestAllowDenyFourCombinations(t *testing.T) {
	// 1. direct allow vs direct deny on the same role: deny wins.
	e := grantFixture(t)
	must(t, e.SetGrant("child", "t", Allow))
	must(t, e.SetGrant("child", "t", Deny))
	if dec := authorize(t, e); dec.Allowed || dec.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("direct allow+deny: got %+v", dec)
	}

	// 2. direct allow vs inherited deny: the closer direct allow wins.
	e = grantFixture(t)
	must(t, e.SetGrant("child", "t", Allow))
	must(t, e.SetGrant("parent", "t", Deny))
	if dec := authorize(t, e); !dec.Allowed {
		t.Fatalf("direct allow vs inherited deny: got %+v", dec)
	}

	// 3. inherited allow vs direct deny: the closer direct deny wins.
	e = grantFixture(t)
	must(t, e.SetGrant("parent", "t", Allow))
	must(t, e.SetGrant("child", "t", Deny))
	if dec := authorize(t, e); dec.Allowed || dec.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("inherited allow vs direct deny: got %+v", dec)
	}

	// 4. inherited allow vs inherited deny at the same distance: deny wins.
	e = grantFixture(t)
	must(t, e.DeclareRole("p2"))
	must(t, e.AddRoleParent("child", "p2"))
	must(t, e.SetGrant("parent", "t", Allow))
	must(t, e.SetGrant("p2", "t", Deny))
	if dec := authorize(t, e); dec.Allowed || dec.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("inherited allow vs inherited deny: got %+v", dec)
	}
}

func TestFinalCombinationAcrossTags(t *testing.T) {
	e := NewEngine()
	must(t, e.DeclareObjectType("A"))
	must(t, e.DeclareTag("t1"))
	must(t, e.DeclareTag("t2"))
	must(t, e.AttachTag("t1", "A"))
	must(t, e.AttachTag("t2", "A"))
	must(t, e.DeclareInstance("i", "A"))
	must(t, e.DeclareRole("r"))
	must(t, e.DeclareSubject("s", "r"))

	// Allow t1, deny t2: explicit deny decides, independent of grant order.
	must(t, e.SetGrant("r", "t1", Allow))
	must(t, e.SetGrant("r", "t2", Deny))
	dec := authorize(t, e)
	if dec.Allowed || dec.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("allow+deny across tags: got %+v", dec)
	}

	// Same declarations registered in the opposite order: same outcome.
	e2 := NewEngine()
	must(t, e2.DeclareObjectType("A"))
	must(t, e2.DeclareTag("t2"))
	must(t, e2.DeclareTag("t1"))
	must(t, e2.AttachTag("t2", "A"))
	must(t, e2.AttachTag("t1", "A"))
	must(t, e2.DeclareInstance("i", "A"))
	must(t, e2.DeclareRole("r"))
	must(t, e2.DeclareSubject("s", "r"))
	must(t, e2.SetGrant("r", "t2", Deny))
	must(t, e2.SetGrant("r", "t1", Allow))
	if dec2 := authorize(t, e2); !reflect.DeepEqual(dec.Tags, dec2.Tags) || dec.Allowed != dec2.Allowed {
		t.Fatalf("registration order changed outcome: %+v vs %+v", dec, dec2)
	}

	// Allow t1, no grant for t2: default-deny with missing-grant reason.
	must(t, e.UnsetGrant("r", "t2"))
	if dec := authorize(t, e); dec.Allowed || dec.Reason != ReasonMissingGrant {
		t.Fatalf("missing grant: got %+v", dec)
	}

	// Allow both: allowed.
	must(t, e.SetGrant("r", "t2", Allow))
	if dec := authorize(t, e); !dec.Allowed {
		t.Fatalf("both allowed: got %+v", dec)
	}
}

func TestRevocationCascadesOnlyThroughSoleSource(t *testing.T) {
	e := chainFixture(t)
	must(t, e.DeclareObjectType("D"))
	must(t, e.DeclareLinkType("dc", "D", "C"))
	must(t, e.AttachTag("t", "A"))
	must(t, e.AttachTag("t", "D"))
	for _, l := range []string{"ab", "bc", "dc"} {
		must(t, e.DeclarePropagation("t", l, Downstream))
	}
	must(t, e.DeclareInstance("ic", "C"))

	// Detach one of two independent sources: C keeps the tag.
	must(t, e.DetachTag("t", "A"))
	if got := tagsOf(t, e, "ic"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("independent source must survive, got %v", got)
	}

	// Detach the last source: the tag disappears downstream.
	must(t, e.DetachTag("t", "D"))
	if got := tagsOf(t, e, "ic"); len(got) != 0 {
		t.Fatalf("expected tag gone, got %v", got)
	}

	// Revoking a propagation qualification cascades the same way.
	must(t, e.AttachTag("t", "A"))
	must(t, e.AttachTag("t", "D"))
	must(t, e.RevokePropagation("t", "dc"))
	if got := tagsOf(t, e, "ic"); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("path via ab/bc must survive, got %v", got)
	}
	must(t, e.RevokePropagation("t", "ab"))
	if got := tagsOf(t, e, "ic"); len(got) != 0 {
		t.Fatalf("expected tag gone after revoking both routes, got %v", got)
	}
}

func TestErrorClassPriority(t *testing.T) {
	// Subject missing wins over everything else.
	e := grantFixture(t)
	must(t, e.DeclareRole("loop1"))
	must(t, e.DeclareRole("loop2", "loop1"))
	must(t, e.AddRoleParent("loop1", "loop2"))
	_, err := e.Authorize("ghost", "i", "read")
	if ClassOf(err) != ClassNotFound {
		t.Fatalf("missing subject must win, got %v", err)
	}

	// All-paths-blocked (class 2) wins over explicit deny (class 3).
	e2 := chainFixture(t)
	must(t, e2.AttachTag("t", "A"))
	must(t, e2.DeclarePropagation("t", "ab", Downstream))
	must(t, e2.DeclareBlock("t", "ab"))
	must(t, e2.DeclareInstance("ib", "B"))
	must(t, e2.DeclareRole("r"))
	must(t, e2.DeclareSubject("s", "r"))
	must(t, e2.SetGrant("r", "t", Deny))
	_, err = e2.CheckTag("s", "ib", "t")
	if ClassOf(err) != ClassAllPathsBlocked {
		t.Fatalf("blocked must win over deny, got %v", err)
	}

	// Explicit deny (class 3) wins over role cycle (class 4): the subject
	// holds one acyclic role with an explicit deny and one cyclic role.
	e3 := grantFixture(t)
	must(t, e3.SetGrant("child", "t", Deny))
	must(t, e3.DeclareRole("c1"))
	must(t, e3.DeclareRole("c2", "c1"))
	must(t, e3.AddRoleParent("c1", "c2"))
	must(t, e3.DeclareSubject("s2", "child", "c1"))
	dec, err := e3.Authorize("s2", "i", "read")
	if err != nil || dec.Allowed || dec.Reason != ReasonExplicitDenyOverride {
		t.Fatalf("explicit deny must win over cycle: dec=%+v err=%v", dec, err)
	}

	// Role cycle (class 4) reported when nothing higher-priority applies.
	e4 := grantFixture(t)
	must(t, e4.DeclareRole("c1"))
	must(t, e4.DeclareRole("c2", "c1"))
	must(t, e4.AddRoleParent("c1", "c2"))
	must(t, e4.DeclareSubject("s3", "c1"))
	_, err = e4.Authorize("s3", "i", "read")
	if ClassOf(err) != ClassRoleCycle {
		t.Fatalf("expected role cycle class, got %v", err)
	}
}

func TestDeniedDecisionHasNoSideEffects(t *testing.T) {
	e := grantFixture(t)
	must(t, e.SetGrant("child", "t", Deny))
	beforeTags := e.typeTags
	beforeBlocked := e.typeBlocked
	beforeGrants := e.roleGrants
	beforeCyclic := e.roleCyclic

	dec, err := e.Authorize("s", "i", "read")
	if err != nil || dec.Allowed {
		t.Fatalf("expected clean denial, got %+v err=%v", dec, err)
	}
	if !reflect.DeepEqual(beforeTags, e.typeTags) ||
		!reflect.DeepEqual(beforeBlocked, e.typeBlocked) ||
		!reflect.DeepEqual(beforeGrants, e.roleGrants) ||
		!reflect.DeepEqual(beforeCyclic, e.roleCyclic) {
		t.Fatal("denied decision mutated derived state")
	}
}
