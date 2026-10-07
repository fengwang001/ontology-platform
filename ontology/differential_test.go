package ontology_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	ont "ontology/ontology"
)

var diffDecls = []ont.PropertyDecl{
	{Name: "id", Type: ont.TypeInt},
	{Name: "ssn", Type: ont.TypeString},
	{Name: "score", Type: ont.TypeFloat},
	{Name: "flag", Type: ont.TypeBool},
}

type op struct {
	kind   string // read|write
	sub    ont.Subject
	inst   string
	proj   []string
	values map[string]ont.RawValue
}

func TestDifferentialAgainstNaive(t *testing.T) {
	const scenarios = 140
	const opsPerScenario = 50
	for seed := 1; seed <= scenarios; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed)))
			ot, err := ont.NewObjectType("Person", diffDecls)
			if err != nil {
				t.Fatal(err)
			}
			rows, props := genPolicies(rng, ot, diffDecls)
			wmode := []ont.WriteMode{ont.WriteRejectAll, ont.WriteDrop}[rng.Intn(2)]
			lg := &ont.SliceLogger{}
			cfg := ont.Config{Type: ot, Rows: rows, Props: props, WriteMode: wmode, Logger: lg}
			eng, gerr := ont.NewEngine(cfg)
			if gerr != nil {
				t.Fatalf("engine construction seed=%d: %v", seed, gerr)
			}
			ref := newNaive(ot, cfg)

			const nInst = 3
			var instIDs []string
			for i := 0; i < nInst; i++ {
				id := "i" + string(rune('a'+i))
				vals := map[string]ont.RawValue{}
				// randomly leave some properties genuinely absent
				for _, d := range diffDecls {
					if rng.Intn(3) != 0 {
						vals[d.Name] = randomValue(rng, d.Type)
					}
				}
				inst, ierr := ot.NewInstance(id, vals)
				if ierr != nil {
					t.Fatal(ierr)
				}
				if aerr := eng.AddInstance(inst); aerr != nil {
					t.Fatal(aerr)
				}
				ref.add(id, vals)
				instIDs = append(instIDs, id)
			}

			for step := 0; step < opsPerScenario; step++ {
				o := genOp(rng, instIDs, step)
				switch o.kind {
				case "read":
					got, gerr := eng.Read(o.sub, o.inst, o.proj)
					want := ref.read(o.sub, o.inst, o.proj)
					assertReadEqual(t, seed, step, o, got, gerr, want)
				case "write":
					got, gerr := eng.Write(o.sub, o.inst, o.values)
					want := ref.write(o.sub, o.inst, o.values)
					assertWriteEqual(t, seed, step, o, got, gerr, want)
					// After every write, raw states must match for
					// every instance as observed by the omnipotent
					// admin subject.
					for _, id := range instIDs {
						gres, gerr2 := eng.Read(ont.Subject{ID: AdminID, Groups: []string{"admin"}}, id, nil)
						rsnap, rok := ref.snapshot(id)
						if gerr2 != nil || !rok {
							t.Fatalf("seed=%d step=%d admin snapshot err=%v", seed, step, gerr2)
						}
						if !rawSnapEqual(gres.View, rsnap) {
							t.Fatalf("seed=%d step=%d raw state diverged after write:\n engine=%v\n naive =%v",
								seed, step, gres.View, rsnap)
						}
					}
				}
			}
		})
	}
}

func genOp(rng *rand.Rand, instIDs []string, step int) op {
	sub := subjects[rng.Intn(len(subjects))]
	id := instIDs[rng.Intn(len(instIDs))]
	if rng.Intn(2) == 0 {
		var proj []string
		switch rng.Intn(4) {
		case 0:
			proj = nil // all
		case 1:
			proj = []string{diffDecls[rng.Intn(len(diffDecls))].Name}
		case 2:
			proj = []string{diffDecls[1].Name, diffDecls[0].Name, diffDecls[1].Name} // dup + order
		case 3:
			if rng.Intn(5) == 0 {
				proj = []string{"ghost"}
			} else {
				proj = []string{diffDecls[0].Name, diffDecls[2].Name}
			}
		}
		return op{kind: "read", sub: sub, inst: id, proj: proj}
	}
	n := 1 + rng.Intn(3)
	values := map[string]ont.RawValue{}
	for i := 0; i < n; i++ {
		d := diffDecls[rng.Intn(len(diffDecls))]
		var v ont.RawValue
		if rng.Intn(6) == 0 {
			v = "wrong-type" // invalid value path
		} else {
			v = randomValue(rng, d.Type)
		}
		values[d.Name] = v
	}
	if rng.Intn(10) == 0 {
		values["no-such-prop"] = int64(1)
	}
	return op{kind: "write", sub: sub, inst: id, values: values}
}

func assertReadEqual(t *testing.T, seed, step int, o op, got *ont.ReadResult, gerr *ont.DecisionError, want naiveOutcome) {
	t.Helper()
	wantErr := want.errKind
	if (gerr == nil) != (wantErr == "") {
		t.Fatalf("seed=%d step=%d read %+v: engine err=%v want %s", seed, step, o, gerr, wantErr)
	}
	if gerr != nil {
		if gerr.Kind.String() != wantErr {
			t.Fatalf("seed=%d step=%d read err kind: got %s want %s", seed, step, gerr.Kind, wantErr)
		}
		return
	}
	if got.Visible != want.visible {
		t.Fatalf("seed=%d step=%d visibility", seed, step)
	}
	gview := map[string]string{}
	for k, v := range got.View {
		tag := "raw:"
		for _, b := range got.Trace.MatchedPropRules {
			if b.Property == k && b.Presented == "masked" {
				tag = "mask:"
			}
		}
		gview[k] = tag + naiveValStr(v)
	}
	if !equalStringMaps(gview, want.view) {
		t.Fatalf("seed=%d step=%d read view diverged:\n engine=%v\n naive =%v", seed, step, got.View, want.view)
	}
	if !eqStrings(got.Redacted, want.redacted) {
		t.Fatalf("seed=%d step=%d redacted diverged: %v vs %v", seed, step, got.Redacted, want.redacted)
	}
	if !eqStrings(got.Absent, want.absent) {
		t.Fatalf("seed=%d step=%d absent diverged: %v vs %v", seed, step, got.Absent, want.absent)
	}
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func assertWriteEqual(t *testing.T, seed, step int, o op, got *ont.WriteResult, gerr *ont.DecisionError, want naiveOutcome) {
	t.Helper()
	if (gerr == nil) != (want.errKind == "") {
		t.Fatalf("seed=%d step=%d write %+v: engine err=%v want %s", seed, step, o, gerr, want.errKind)
	}
	if gerr != nil {
		if gerr.Kind.String() != want.errKind {
			t.Fatalf("seed=%d step=%d write err kind: got %s want %s (values=%v)",
				seed, step, gerr.Kind, want.errKind, o.values)
		}
		return
	}
	if !eqStrings(got.Applied, want.applied) {
		t.Fatalf("seed=%d step=%d applied: %v vs %v", seed, step, got.Applied, want.applied)
	}
	if !eqStrings(got.Dropped, want.dropped) {
		t.Fatalf("seed=%d step=%d dropped: %v vs %v", seed, step, got.Dropped, want.dropped)
	}
	if got.Version != want.version {
		t.Fatalf("seed=%d step=%d version: %d vs %d", seed, step, got.Version, want.version)
	}
}

func eqStrings(a, b []string) bool {
	a = sortedCopy(a)
	b = sortedCopy(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rawSnapEqual(got map[string]ont.RawValue, want map[string]ont.RawValue) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		g, ok := got[k]
		if !ok || naiveValStr(g) != naiveValStr(v) {
			return false
		}
	}
	return true
}

var _ = sort.Strings
