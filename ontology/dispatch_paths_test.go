package ontology

import (
	"context"
	"errors"
	"testing"
)

// TestDispatchPaths covers the full combination matrix of the three dispatch
// paths: direct registration, inherited hit, explicit waive (with and without
// an ancestor implementation), plus the two distinct no-dispatch states.
func TestDispatchPaths(t *testing.T) {
	type tc struct {
		name      string
		concrete  entryState
		p1        entryState
		p2        entryState
		concreteW bool // explicit waive on concrete
		p1W       bool
		wantOut   string
		wantClass DispatchErrorClass
		wantBasis PathBasis
		wantOwner TypeID
		wantErr   error
	}

	cases := []tc{
		{
			name:     "direct registration wins over inherited",
			concrete: stateRegistered, p1: stateRegistered,
			wantOut: "c", wantBasis: BasisDirect, wantOwner: "concrete",
		},
		{
			name:    "inherited hit when concrete absent",
			p1:      stateRegistered,
			wantOut: "p1", wantBasis: BasisInherited, wantOwner: "p1",
		},
		{
			name:    "inherited hit from deeper ancestor",
			p2:      stateRegistered,
			wantOut: "p2", wantBasis: BasisInherited, wantOwner: "p2",
		},
		{
			name:      "waive on concrete skips to ancestor impl",
			concreteW: true, p1: stateRegistered,
			wantOut: "p1", wantBasis: BasisInherited, wantOwner: "p1",
		},
		{
			name:      "waive mid-chain continues upward",
			concreteW: true, p1W: true, p2: stateRegistered,
			wantOut: "p2", wantBasis: BasisInherited, wantOwner: "p2",
		},
		{
			name:      "nothing registered -> no dispatch",
			wantClass: ClassNoDispatch, wantErr: ErrNoImplementation,
		},
		{
			name:      "waive with no ancestor impl -> waived no dispatch",
			concreteW: true,
			wantClass: ClassWaivedNoDispatch, wantErr: ErrExplicitlyWaived,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg, d, a := newSetup()
			_, _, _, _, concrete := hierarchy()

			if c.concrete == stateRegistered {
				mustReg(t, reg, a.ID, "concrete", mkImpl("c"))
			}
			if c.p1 == stateRegistered {
				mustReg(t, reg, a.ID, "p1", mkImpl("p1"))
			}
			if c.p2 == stateRegistered {
				mustReg(t, reg, a.ID, "p2", mkImpl("p2"))
			}
			if c.concreteW {
				reg.Waive(a.ID, "concrete")
			}
			if c.p1W {
				reg.Waive(a.ID, "p1")
			}

			obj := &Instance{ID: "o1", Type: concrete}
			out, tr, err := d.Invoke(context.Background(), obj, a.ID, "x")

			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("want error %v, got %v", c.wantErr, err)
				}
				if tr.ErrorClass != c.wantClass {
					t.Fatalf("want class %d, got %d", c.wantClass, tr.ErrorClass)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != c.wantOut {
				t.Fatalf("want output %q, got %v", c.wantOut, out)
			}
			if tr.HitBasis != c.wantBasis || tr.OwnerType != c.wantOwner {
				t.Fatalf("want basis=%d owner=%s, got basis=%d owner=%s",
					c.wantBasis, c.wantOwner, tr.HitBasis, tr.OwnerType)
			}
			if c.concreteW && !tr.SawWaive {
				t.Fatal("trace should record observed waive")
			}
		})
	}
}

func mustReg(t *testing.T, reg *Registry, a ActionID, typ TypeID, impl Implementation) {
	t.Helper()
	if _, err := reg.Register(a, typ, impl, nil); err != nil {
		t.Fatalf("register %s: %v", typ, err)
	}
}

// TestAbsentVsWaiveDistinguishable asserts the two states never conflate in
// registry snapshots.
func TestAbsentVsWaiveDistinguishable(t *testing.T) {
	reg, _, a := newSetup()
	_, _, _, _, concrete := hierarchy()

	st, _ := reg.snapshotLocked(a.ID, concrete.ID)
	if st != stateAbsent {
		t.Fatalf("want absent, got %d", st)
	}
	reg.Waive(a.ID, concrete.ID)
	st, _ = reg.snapshotLocked(a.ID, concrete.ID)
	if st != stateWaived {
		t.Fatalf("want waived, got %d", st)
	}
}

// TestNoDispatchPrecedesRevocation asserts undispatchable cases are judged
// before revocation, even when the object was revoked before the call.
func TestNoDispatchPrecedesRevocation(t *testing.T) {
	_, d, a := newSetup()
	_, _, _, _, concrete := hierarchy()
	obj := &Instance{ID: "o", Type: concrete}
	obj.Revoke()

	_, tr, err := d.Invoke(context.Background(), obj, a.ID, "x")
	if !errors.Is(err, ErrNoImplementation) {
		t.Fatalf("want no-implementation precedence, got %v", err)
	}
	if tr.ErrorClass != ClassNoDispatch {
		t.Fatalf("want ClassNoDispatch, got %d", tr.ErrorClass)
	}
}

// TestPrePostConditions verifies condition failures only occur after a
// successful dispatch.
func TestPrePostConditions(t *testing.T) {
	reg, d, a := newSetup()
	_, _, _, _, concrete := hierarchy()

	rejecting := &funcImpl{name: "r", pre: func(any) bool { return false }}
	mustReg(t, reg, a.ID, concrete.ID, rejecting)
	obj := &Instance{ID: "o", Type: concrete}
	_, tr, err := d.Invoke(context.Background(), obj, a.ID, "x")
	if !errors.Is(err, ErrPrecondition) || tr.ErrorClass != ClassCondition {
		t.Fatalf("want precondition failure, got %v class=%d", err, tr.ErrorClass)
	}

	reg2 := NewRegistry()
	d2 := NewDispatcher(reg2)
	reg2.DeclareAction(a)
	badPost := &funcImpl{
		name: "bp",
		post: func(any, any) bool { return false },
	}
	mustReg(t, reg2, a.ID, concrete.ID, badPost)
	_, tr, err = d2.Invoke(context.Background(), obj, a.ID, "x")
	if !errors.Is(err, ErrPostcondition) || tr.ErrorClass != ClassCondition {
		t.Fatalf("want postcondition failure, got %v class=%d", err, tr.ErrorClass)
	}
}
