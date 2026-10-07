package derived

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// textLogger prints every processing unit: input, affected downstream set and
// the decision basis, as required for audit.
type textLogger struct {
	sb strings.Builder
}

func (l *textLogger) Log(r ChangeRecord) {
	fmt.Fprintf(&l.sb, "OP %s input=%v", r.Op, r.Input)
	if len(r.Affected) > 0 {
		fmt.Fprintf(&l.sb, " affected=%v", r.Affected)
	}
	if r.Basis != "" {
		fmt.Fprintf(&l.sb, " basis=%q", r.Basis)
	}
	if r.RolledBack {
		fmt.Fprintf(&l.sb, " ROLLED_BACK kind=%d msg=%s", r.Err.Kind, r.Err.Message)
	}
	l.sb.WriteString("\n")
	for _, e := range r.Entries {
		fmt.Fprintf(&l.sb, "    entry %s@%s state=%s keys=%v\n",
			e.Declaration, e.ObjectID, e.State, e.Keys)
	}
}

type fixture struct {
	types     []string
	objects   []string
	objType   map[string]string
	decls     []Declaration
	declNames []string
	baseProps map[string][]string // decl name -> base property used in chains
}

func buildFixture(rng *rand.Rand) (*Store, *NaiveModel, *fixture, *textLogger) {
	lg := &textLogger{}
	s := NewStore(lg)
	n := NewNaiveModel()
	fx := &fixture{
		types:     []string{"T0", "T1", "T2", "T3"},
		objType:   map[string]string{},
		baseProps: map[string][]string{},
	}

	// Objects of each type.
	for _, ty := range fx.types {
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("%s%d", ty, i)
			o := Object{ID: id, Type: ty,
				Properties: map[string]Value{"p": fmt.Sprintf("%s-init", id)}}
			_ = s.AddObject(o)
			n.AddObject(o)
			fx.objects = append(fx.objects, id)
			fx.objType[id] = ty
		}
	}

	// One single-level declaration for every ordered type pair via a unique
	// link type, plus a few chained declarations to exercise multi-level
	// propagation (only chains that cannot type-loop: type index decreases).
	for _, ds := range fx.types {
		for _, sr := range fx.types {
			name := fmt.Sprintf("d_%s_%s", ds, sr)
			d := Declaration{Name: name, DownstreamType: ds,
				LinkType:       fmt.Sprintf("l_%s_%s", ds, sr),
				SourceType:     sr,
				SourceProperty: "p",
				RequireUnique:  rng.Intn(2) == 0,
			}
			if err := s.AddDeclaration(d); err != nil {
				panic(err)
			}
			n.AddDeclaration(d)
			fx.decls = append(fx.decls, d)
			fx.declNames = append(fx.declNames, name)
			fx.baseProps[name] = []string{"p"}
		}
	}
	// Chained declarations: T2 derives via l_T2_T1 from a T1 declaration,
	// T3 derives via l_T3_T2 from that chained declaration.
	chain2 := Declaration{Name: "chain2", DownstreamType: "T2",
		LinkType: "cl_21", SourceType: "T1",
		SourceProperty: "d_T1_T0", RequireUnique: true}
	chain3 := Declaration{Name: "chain3", DownstreamType: "T3",
		LinkType: "cl_32", SourceType: "T2",
		SourceProperty: "chain2", RequireUnique: true}
	for _, d := range []Declaration{chain2, chain3} {
		if err := s.AddDeclaration(d); err != nil {
			panic(err)
		}
		n.AddDeclaration(d)
		fx.decls = append(fx.decls, d)
		fx.declNames = append(fx.declNames, d.Name)
	}

	return s, n, fx, lg
}

func sameEntries(a, b []Entry) bool {
	key := func(e Entry) string {
		k := append([]string(nil), e.Keys...)
		sort.Strings(k)
		return fmt.Sprintf("%s@%s:%d:%v", e.Declaration, e.ObjectID, e.State, k)
	}
	x := make([]string, len(a))
	for i, e := range a {
		x[i] = key(e)
	}
	y := make([]string, len(b))
	for i, e := range b {
		y[i] = key(e)
	}
	sort.Strings(x)
	sort.Strings(y)
	return reflect.DeepEqual(x, y)
}

// TestRandomDifferential drives thousands of random property writes and link
// add/delete operations through the store and mirrors committed effects on
// the independent oracle. After every step: full entry sets, key lookups and
// exact-affected-set counts must agree.
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	s, n, fx, lg := buildFixture(rng)

	check := func(step int) {
		t.Helper()
		got := s.Snapshot().AllEntries()
		want := n.AllEntries()
		if !sameEntries(got, want) {
			t.Fatalf("step %d entry mismatch\nGOT %+v\nWANT %+v\nLOG:\n%s",
				step, got, want, lg.sb.String())
		}
		for _, name := range fx.declNames {
			for _, val := range []string{"p", "red", "green", "blue", "v1", "x"} {
				g := s.Lookup(name, val)
				w := n.Lookup(name, val)
				if !reflect.DeepEqual(g, w) {
					t.Fatalf("step %d lookup %s=%v got %v want %v",
						step, name, val, g, w)
				}
			}
		}
	}

	for step := 0; step < 6000; step++ {
		switch rng.Intn(5) {
		case 0, 1:
			obj := pickID(rng, fx.objects)
			val := []string{"red", "green", "blue", "v1"}[rng.Intn(4)]
			r := s.SetProperty(obj, "p", val)
			oracleBefore := n.DownstreamSet(obj, "p")
			if r.Err != nil && r.Err.Kind != KindSourceNotFound {
				t.Fatalf("unexpected set error: %v", r.Err)
			}
			if r.Committed {
				if len(r.Affected) != len(oracleBefore) {
					t.Fatalf("step %d affected %d != oracle %d\nLOG:\n%s",
						step, len(r.Affected), len(oracleBefore), lg.sb.String())
				}
				n.SetProperty(obj, "p", val)
			}

		case 2, 3:
			d := fx.decls[rng.Intn(len(fx.decls))]
			froms, tos := objectsOfType(fx, d.DownstreamType), objectsOfType(fx, d.SourceType)
			from, to := pickID(rng, froms), pickID(rng, tos)
			if rng.Intn(2) == 0 {
				r := s.AddLink(from, to, d.LinkType)
				if r.Committed {
					n.AddLink(from, to, d.LinkType)
				} else if r.Err != nil && r.Err.Kind != KindCycleDetected {
					t.Fatalf("step %d unexpected add-link error %v", step, r.Err)
				}
			} else {
				r := s.DeleteLink(from, to, d.LinkType)
				if r.Committed {
					n.DeleteLink(from, to, d.LinkType)
				} else if r.Err != nil && r.Err.Kind != KindSourceNotFound {
					t.Fatalf("step %d unexpected del-link error %v", step, r.Err)
				}
			}

		case 4:
			// Delete an object and re-add it to keep the population stable.
			id := pickID(rng, fx.objects)
			r := s.DeleteObject(id)
			if r.Committed {
				n.DeleteObject(id)
				o := Object{ID: id, Type: fx.objType[id],
					Properties: map[string]Value{"p": id + "-reborn"}}
				if err := s.AddObject(o); err != nil {
					t.Fatal(err)
				}
				n.AddObject(o)
			}
		}
		check(step)
	}

	// Emit a sample of the audit log so the requirement is visibly exercised.
	log := lg.sb.String()
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	limit := 30
	if len(lines) < limit {
		limit = len(lines)
	}
	t.Logf("recorded %d log lines; first %d:\n%s", len(lines), limit,
		strings.Join(lines[:limit], "\n"))
	if !strings.Contains(log, "basis=") {
		t.Fatal("log must record decision basis")
	}
	if !strings.Contains(log, "affected=") {
		t.Fatal("log must record affected downstream sets")
	}
}

func objectsOfType(fx *fixture, ty string) []string {
	var out []string
	for _, id := range fx.objects {
		if fx.objType[id] == ty {
			out = append(out, id)
		}
	}
	return out
}

func pickID(rng *rand.Rand, slice []string) string { return slice[rng.Intn(len(slice))] }

// TestRandomDifferentialInjectedFailures verifies rollback: the store must
// diverge from the oracle only on units that abort, and stay equal once the
// failing hook is cleared (aborted units never mutate committed state).
func TestRandomDifferentialInjectedFailures(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	s, n, fx, _ := buildFixture(rng)
	s.FailHook = func(decl, id string) bool { return rng.Intn(7) == 0 }

	for step := 0; step < 2000; step++ {
		d := fx.decls[rng.Intn(len(fx.decls))]
		from := pickID(rng, objectsOfType(fx, d.DownstreamType))
		to := pickID(rng, objectsOfType(fx, d.SourceType))
		r := s.AddLink(from, to, d.LinkType)
		if r.Committed {
			n.AddLink(from, to, d.LinkType)
		}
		obj := pickID(rng, fx.objects)
		r2 := s.SetProperty(obj, "p", "x")
		if r2.Committed {
			n.SetProperty(obj, "p", "x")
		}
	}
	s.FailHook = nil
	got := s.Snapshot().AllEntries()
	want := n.AllEntries()
	if !sameEntries(got, want) {
		t.Fatalf("state after aborted units must equal oracle of committed ops")
	}
}
