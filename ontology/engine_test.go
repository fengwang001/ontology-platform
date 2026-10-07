package ontology_test

import (
	"io"
	"math"
	"sync"
	"testing"

	"ontology/ontology"
)

func newEngine(t *testing.T, policy ontology.ContributionPolicy) (*ontology.Store, *ontology.Engine) {
	t.Helper()
	s := ontology.NewStore()
	e := ontology.NewEngine(s)
	must(t, e.RegisterView(ontology.ViewDef{
		Name: "sales_by_region", GroupType: "region", AggType: "sale",
		LinkType: "sale_in_region", ValueProperty: "amount", Contribution: policy,
	}))
	return s, e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErrClass(t *testing.T, err error, class ontology.ErrorClass) {
	t.Helper()
	opErr, ok := err.(*ontology.OpError)
	if !ok {
		t.Fatalf("want *OpError, got %T: %v", err, err)
	}
	if opErr.Class != class {
		t.Fatalf("error class = %d, want %d: %v", opErr.Class, class, err)
	}
}

func assertAgg(t *testing.T, e *ontology.Engine, view, group string, sum float64, count int) {
	t.Helper()
	got, err := e.Query(view, group)
	must(t, err)
	if math.Abs(got.Sum-sum) > 1e-9 || got.Count != count {
		t.Fatalf("group %s = (sum=%v,count=%d), want (sum=%v,count=%d)", group, got.Sum, got.Count, sum, count)
	}
}

func TestFullEachContributesToEveryGroup(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyFullEach)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("region", "south"))
	must(t, s.CreateObject("sale", "s1"))

	_, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 10})
	must(t, err)
	_, err = e.AddToGroup("sales_by_region", "s1", "north")
	must(t, err)
	_, err = e.AddToGroup("sales_by_region", "s1", "south")
	must(t, err)

	assertAgg(t, e, "sales_by_region", "north", 10, 1)
	assertAgg(t, e, "sales_by_region", "south", 10, 1)

	res, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 15})
	must(t, err)
	if res.TouchedCount != 2 {
		t.Fatalf("property write touched %d groups, want exactly 2", res.TouchedCount)
	}
	assertAgg(t, e, "sales_by_region", "north", 15, 1)
	assertAgg(t, e, "sales_by_region", "south", 15, 1)
}

func TestDenyMultiRejectsSecondMembership(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyDenyMulti)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("region", "south"))
	must(t, s.CreateObject("sale", "s1"))
	_, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 10})
	must(t, err)
	_, err = e.AddToGroup("sales_by_region", "s1", "north")
	must(t, err)

	_, err = e.AddToGroup("sales_by_region", "s1", "south")
	wantErrClass(t, err, ontology.ClassPolicyRejected)

	assertAgg(t, e, "sales_by_region", "north", 10, 1)
	assertAgg(t, e, "sales_by_region", "south", 0, 0)
}

func TestAbsentVsZero(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyFullEach)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("sale", "s1"))
	must(t, s.CreateObject("sale", "s2"))
	_, err := e.AddToGroup("sales_by_region", "s1", "north")
	must(t, err)
	_, err = e.AddToGroup("sales_by_region", "s2", "north")
	must(t, err)

	assertAgg(t, e, "sales_by_region", "north", 0, 0)

	_, err = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 0})
	must(t, err)
	assertAgg(t, e, "sales_by_region", "north", 0, 1)

	_, err = e.SetMemberProperty("sales_by_region", "s2", ontology.Optional{Present: true, Value: 7})
	must(t, err)
	assertAgg(t, e, "sales_by_region", "north", 7, 2)

	_, err = e.SetMemberProperty("sales_by_region", "s2", ontology.Optional{})
	must(t, err)
	assertAgg(t, e, "sales_by_region", "north", 0, 1)
}

func TestReparentConstantTouches(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyDenyMulti)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("region", "south"))
	must(t, s.CreateObject("sale", "s1"))
	_, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 10})
	must(t, err)
	_, err = e.AddToGroup("sales_by_region", "s1", "north")
	must(t, err)

	ver, err := e.MembersVersion("sales_by_region", "s1")
	must(t, err)
	res, err := e.Reparent("sales_by_region", "s1", "south", ver)
	must(t, err)

	if res.TouchedCount != 2 {
		t.Fatalf("reparent touched %d groups, want exactly 2", res.TouchedCount)
	}
	assertAgg(t, e, "sales_by_region", "north", 0, 0)
	assertAgg(t, e, "sales_by_region", "south", 10, 1)

	_, err = e.Reparent("sales_by_region", "s1", "north", ver)
	wantErrClass(t, err, ontology.ClassConcurrentConflict)
	assertAgg(t, e, "sales_by_region", "south", 10, 1)
	assertAgg(t, e, "sales_by_region", "north", 0, 0)
}

func TestDeleteCascades(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyFullEach)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("sale", "s1"))
	must(t, s.CreateObject("sale", "s2"))
	_, _ = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 3})
	_, _ = e.SetMemberProperty("sales_by_region", "s2", ontology.Optional{Present: true, Value: 4})
	_, _ = e.AddToGroup("sales_by_region", "s1", "north")
	_, _ = e.AddToGroup("sales_by_region", "s2", "north")
	assertAgg(t, e, "sales_by_region", "north", 7, 2)

	_, err := e.DeleteMember("sales_by_region", "s1")
	must(t, err)
	assertAgg(t, e, "sales_by_region", "north", 4, 1)

	_, err = e.DeleteGroup("sales_by_region", "north")
	must(t, err)
	if !s.ObjectExists("sale", "s2") {
		t.Fatal("aggregated instance must survive group deletion")
	}
	_, err = e.Query("sales_by_region", "north")
	wantErrClass(t, err, ontology.ClassGroupMissing)
}

func TestProcessingUnitRollback(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyFullEach)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("sale", "s1"))
	_, _ = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 10})
	_, _ = e.AddToGroup("sales_by_region", "s1", "north")

	e.SetFailHook(func(view, op string) error { return io.EOF })
	_, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 99})
	wantErrClass(t, err, ontology.ClassTxnFailed)
	e.SetFailHook(nil)

	assertAgg(t, e, "sales_by_region", "north", 10, 1)
	if got, _ := s.GetProperty("sale", "s1", "amount"); got.Value != 10 {
		t.Fatalf("store property must roll back, got %v", got)
	}
}

func TestErrorPriority(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyDenyMulti)
	_, err := e.AddToGroup("sales_by_region", "ghost", "nowhere")
	wantErrClass(t, err, ontology.ClassGroupMissing)

	must(t, s.CreateObject("region", "north"))
	_, err = e.AddToGroup("sales_by_region", "ghost", "north")
	wantErrClass(t, err, ontology.ClassTypeNotParticipating)

	must(t, s.CreateObject("region", "south"))
	must(t, s.CreateObject("sale", "s1"))
	_, _ = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 1})
	_, _ = e.AddToGroup("sales_by_region", "s1", "north")
	e.SetFailHook(func(view, op string) error {
		t.Error("fail hook must not run when the unit is rejected as a conflict")
		return nil
	})
	_, err = e.Reparent("sales_by_region", "s1", "south", 999)
	wantErrClass(t, err, ontology.ClassConcurrentConflict)
	e.SetFailHook(nil)
}

func TestConcurrentWriteAndMoveSerializability(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyDenyMulti)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("region", "south"))
	must(t, s.CreateObject("sale", "s1"))
	_, _ = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 10})
	_, _ = e.AddToGroup("sales_by_region", "s1", "north")

	ver, _ := e.MembersVersion("sales_by_region", "s1")
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	record := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	wg.Add(2)
	go func() { defer wg.Done(); _, err := e.Reparent("sales_by_region", "s1", "south", ver); record(err) }()
	go func() {
		defer wg.Done()
		_, err := e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 20})
		record(err)
	}()
	wg.Wait()
	must(t, firstErr)

	assertAgg(t, e, "sales_by_region", "north", 0, 0)
	assertAgg(t, e, "sales_by_region", "south", 20, 1)
	groups := s.Memberships("sale_in_region", "s1")
	if len(groups) != 1 || groups[0] != "south" {
		t.Fatalf("final membership = %v, want [south]", groups)
	}
}

func TestConcurrentReparentOneWinner(t *testing.T) {
	s, e := newEngine(t, ontology.PolicyDenyMulti)
	must(t, s.CreateObject("region", "north"))
	must(t, s.CreateObject("region", "east"))
	must(t, s.CreateObject("region", "west"))
	must(t, s.CreateObject("sale", "s1"))
	_, _ = e.SetMemberProperty("sales_by_region", "s1", ontology.Optional{Present: true, Value: 5})
	_, _ = e.AddToGroup("sales_by_region", "s1", "north")
	ver, _ := e.MembersVersion("sales_by_region", "s1")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = e.Reparent("sales_by_region", "s1", "east", ver) }()
	go func() { defer wg.Done(); _, errs[1] = e.Reparent("sales_by_region", "s1", "west", ver) }()
	wg.Wait()

	accepted, rejected := 0, 0
	for _, err := range errs {
		if err == nil {
			accepted++
		} else {
			wantErrClass(t, err, ontology.ClassConcurrentConflict)
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d, want 1/1", accepted, rejected)
	}
	groups := s.Memberships("sale_in_region", "s1")
	if len(groups) != 1 {
		t.Fatalf("final memberships = %v, want exactly one group", groups)
	}
	assertAgg(t, e, "sales_by_region", groups[0], 5, 1)
	assertAgg(t, e, "sales_by_region", "north", 0, 0)
}
