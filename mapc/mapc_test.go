package mapc

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/sch"
)

// evolvedReg builds the six-version evolution from NOTES.md.
func evolvedReg() *sch.Registry {
	r := sch.NewRegistry(
		sch.Column{Name: "x", Typ: sch.Int, Required: true},
		sch.Column{Name: "y", Typ: sch.Str, Required: true},
		sch.Column{Name: "m", Typ: sch.Str, Required: true},
	)
	r.AddColumn("z", sch.Str, true)
	r.ChangeType("y", sch.Int)
	r.ChangeType("x", sch.Str)
	r.DropColumn("m")
	r.AddColumn("w", sch.Int, false)
	return r
}

// naive is the obviously-correct reference: linear scan by name per column.
func naive(event, active []sch.Column, values []any) ([]any, error) {
	out := make([]any, len(active))
	for i, ac := range active {
		j := -1
		for k, ec := range event {
			if ec.Name == ac.Name {
				j = k
			}
		}
		if j < 0 {
			if ac.Required {
				return nil, sch.ErrMissingColumn
			}
			out[i] = sch.Zero(ac.Typ)
			continue
		}
		v, err := sch.Coerce(values[j], event[j].Typ, ac.Typ)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// TestLocateComparisonsConstant proves name lookup is O(1): the per-column
// locate comparison count must not grow with the layout width m.
func TestLocateComparisonsConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		layout := make([]sch.Column, m)
		vals := make([]any, m)
		for i := range layout {
			layout[i] = sch.Column{Name: fmt.Sprint("c", i), Typ: sch.Int, Required: true}
			vals[i] = int64(i)
		}
		mp := NewMapper()
		got, err := mp.Map(layout, layout, vals)
		if err != nil || len(got) != m {
			t.Fatalf("m=%d: Map = %v, %v", m, got, err)
		}
		if c := mp.locateCmp.Load(); c > 2 { // hash locate: constant, not O(m)
			t.Fatalf("m=%d: per-column locate comparisons = %d, want <= 2", m, c)
		}
	}
}

// TestMapAlignsByName pins name (not position) alignment: dropped columns
// vanish, reordered columns follow their names, new optional columns zero.
func TestMapAlignsByName(t *testing.T) {
	event := []sch.Column{
		{Name: "x", Typ: sch.Int}, {Name: "y", Typ: sch.Str}, {Name: "m", Typ: sch.Str},
	}
	active := []sch.Column{
		{Name: "y", Typ: sch.Int}, {Name: "x", Typ: sch.Str}, {Name: "w", Typ: sch.Int},
	}
	got, err := NewMapper().Map(event, active, []any{int64(5), "6", "M"})
	want := []any{int64(6), "5", int64(0)}
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

// TestMapDeterministic: same inputs, same result, every call.
func TestMapDeterministic(t *testing.T) {
	reg := evolvedReg()
	ev, active, _ := reg.Snapshot(2)
	vals := []any{int64(5), "6", "M", "b"}
	first, err := NewMapper().Map(ev, active, vals)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := NewMapper().Map(ev, active, vals)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("run %d: %v, %v", i, got, err)
		}
	}
}

// TestMapMatchesNaive: Map agrees with the naive reference on random events
// of every registered version, column by column.
func TestMapMatchesNaive(t *testing.T) {
	reg := evolvedReg()
	rng := rand.New(rand.NewSource(1))
	for ver := 1; ver <= 6; ver++ {
		ev, active, err := reg.Snapshot(ver)
		if err != nil {
			t.Fatal(err)
		}
		for trial := 0; trial < 20; trial++ {
			vals := make([]any, len(ev))
			for j, c := range ev {
				if c.Typ == sch.Int {
					vals[j] = int64(rng.Intn(100))
				} else if rng.Intn(4) == 0 {
					vals[j] = "junk" // sometimes unparseable
				} else {
					vals[j] = fmt.Sprint(rng.Intn(100))
				}
			}
			got, gerr := NewMapper().Map(ev, active, vals)
			want, werr := naive(ev, active, vals)
			if gerr != werr || fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("v%d %v: Map=%v,%v naive=%v,%v", ver, vals, got, gerr, want, werr)
			}
		}
	}
}
