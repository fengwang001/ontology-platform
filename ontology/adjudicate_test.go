package ontology

import (
	"bytes"
	"testing"
)

const empType = "Employee"

func employeeType() *ObjectType {
	return &ObjectType{
		Name: empType,
		Properties: []PropertyDef{
			{Name: "name", Type: DeclaredType{Kind: KindString}},
			{Name: "salary", Type: DeclaredType{Kind: KindInt, Min: ptrFloat(0)}},
			{Name: "clearance", Type: DeclaredType{Kind: KindString}},
			{Name: "band", Type: DeclaredType{Kind: KindString, EnumValues: []string{"low", "high", "redacted"}}},
			{Name: "active", Type: DeclaredType{Kind: KindBoolean}},
		},
	}
}

type harness struct {
	a       *Adjudicator
	catalog *PolicyCatalog
	store   *Store
	buf     *bytes.Buffer
}

func newHarness(t *testing.T, cfg Config) harness {
	t.Helper()
	catalog := NewPolicyCatalog()
	store := NewStore()
	buf := &bytes.Buffer{}
	audit := NewAuditLogger(buf)
	a := NewAdjudicator(cfg, catalog, store, audit)
	a.RegisterType(employeeType())
	return harness{a: a, catalog: catalog, store: store, buf: buf}
}

func allowDeny() (*Effect, *Effect) {
	al := EffectAllow
	de := EffectDeny
	return &al, &de
}

func mustRegRow(t *testing.T, c *PolicyCatalog, p RowPolicy) {
	t.Helper()
	if err := c.RegisterRowPolicy(p); err != nil {
		t.Fatal(err)
	}
}

func idContains(ids []string, want string) bool {
	for _, v := range ids {
		if v == want {
			return true
		}
	}
	return false
}

func denyByDefault() Config {
	return Config{
		RowMode: DenyOverrides, PropertyMode: DenyOverrides,
		DefaultRow: EffectDeny, DefaultRead: EffectDeny, DefaultWrite: EffectDeny,
	}
}

func TestRowPredicateIgnoresReadMaskAndReadDeny(t *testing.T) {
	al, de := allowDeny()
	h := newHarness(t, denyByDefault())
	h.store.Put(Instance{
		Type: empType, ID: "e1",
		Values: map[string]Value{
			"name": {Str: "Ada"}, "salary": {Int: 900}, "clearance": {Str: "high"},
		},
	})
	mustRegRow(t, h.catalog, RowPolicy{
		ID: "row-high", ObjectType: empType, Subjects: []string{"bob"},
		Effect: EffectAllow,
		Predicate: Predicate{Atoms: []Atom{
			{Property: "clearance", Op: OpEq, Str: "high"},
		}},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "deny-clearance", ObjectType: empType, Subjects: []string{"bob"},
		Property: "clearance", Read: de,
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "read-name", ObjectType: empType, Subjects: []string{"bob"},
		Property: "name", Read: al,
	})

	view, err := h.a.Read("bob", empType, "e1")
	if err != nil {
		t.Fatalf("row must be visible via unreadable property: %v", err)
	}
	if _, present := view.Fields["clearance"]; present {
		t.Fatal("denied property must not appear in the view")
	}
	if view.Status["clearance"] != AbsentUnreadable {
		t.Fatalf("clearance status = %s", view.Status["clearance"])
	}
	if view.Fields["name"].Value.Str != "Ada" {
		t.Fatal("readable name must carry raw value")
	}
	if !idContains(view.Basis.MatchedRow, "row-high") {
		t.Fatal("basis must cite the matched row policy")
	}
}

func TestThreeAbsenceStatesAreDistinguishable(t *testing.T) {
	_, de := allowDeny()
	cfg := Config{
		RowMode: AllowOverrides, PropertyMode: AllowOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
	}
	h := newHarness(t, cfg)
	// salary is explicitly present with the zero value of int64; clearance is
	// declared but never stored; active is present but unreadable.
	h.store.Put(Instance{
		Type:    empType,
		ID:      "e2",
		Values:  map[string]Value{"name": {Str: ""}, "salary": {Int: 0}, "active": {Bool: false}},
		Present: map[string]bool{"name": true, "salary": true, "active": true},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "deny-active", ObjectType: empType, Subjects: []string{"sue"},
		Property: "active", Read: de,
	})

	view, err := h.a.Read("sue", empType, "e2")
	if err != nil {
		t.Fatal(err)
	}
	fv, ok := view.Fields["salary"]
	if !ok || fv.Value.Int != 0 {
		t.Fatalf("zero salary must be present as zero, got %v ok=%v", fv, ok)
	}
	if view.Status["salary"] != PresentReadable {
		t.Fatalf("salary status = %s", view.Status["salary"])
	}
	if view.Status["active"] != AbsentUnreadable {
		t.Fatalf("active status = %s", view.Status["active"])
	}
	if _, present := view.Fields["active"]; present {
		t.Fatal("unreadable field must be omitted from Fields")
	}
	if view.Status["clearance"] != AbsentNotStored {
		t.Fatalf("clearance status = %s", view.Status["clearance"])
	}
	if _, present := view.Fields["clearance"]; present {
		t.Fatal("not-stored field must be absent from Fields")
	}
	if got := view.StatusFor("nope"); got != AbsentNotDeclared {
		t.Fatalf("undeclared status = %s", got)
	}
}

func TestMaskedValueAndMaskTypeViolation(t *testing.T) {
	al, _ := allowDeny()
	cfg := Config{
		RowMode: AllowOverrides, PropertyMode: AllowOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
	}
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type: empType,
		ID:   "e3",
		Values: map[string]Value{
			"name": {Str: "Grace"}, "clearance": {Str: "high"}, "band": {Str: "high"},
		},
		Present: map[string]bool{"name": true, "clearance": true, "band": true},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "mask-clearance", ObjectType: empType, Property: "clearance",
		Read: al, MaskName: "redact",
		Mask: func(Value) Value { return Value{Str: "REDACTED"} },
	})
	view, err := h.a.Read("anyone", empType, "e3")
	if err != nil {
		t.Fatal(err)
	}
	fv := view.Fields["clearance"]
	if !fv.Masked || fv.Value.Str != "REDACTED" {
		t.Fatalf("masked value = %+v", fv)
	}

	// A mask on the enum-typed band whose output violates the contract
	// produces a distinguished read error and no view.
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "bad-band-mask", ObjectType: empType, Property: "band",
		Read: al, MaskName: "bogus",
		Mask: func(Value) Value { return Value{Str: "whatever"} },
	})
	_, err = h.a.Read("anyone", empType, "e3")
	if err == nil || err.Kind != ErrMaskTypeViolation {
		t.Fatalf("want mask type violation, got %v", err)
	}
	// Conflict is local to this instance/property: another instance reads.
	h.store.Put(Instance{
		Type: empType,
		ID:   "e4",
		Values: map[string]Value{
			"name": {Str: "Lin"}, "clearance": {Str: "low"},
		},
		Present: map[string]bool{"name": true, "clearance": true},
	})
	if _, err := h.a.Read("anyone", empType, "e4"); err != nil {
		t.Fatalf("conflict on e3 must not affect e4: %v", err)
	}
}

func TestRowInvisibleHidesExistence(t *testing.T) {
	h := newHarness(t, denyByDefault())
	h.store.Put(Instance{
		Type:    empType,
		ID:      "secret",
		Values:  map[string]Value{"name": {Str: "X"}},
		Present: map[string]bool{"name": true},
	})
	if _, err := h.a.Read("nobody", empType, "secret"); err == nil || err.Kind != ErrRowInvisible {
		t.Fatalf("existing hidden: %v", err)
	}
	if _, err := h.a.Read("nobody", empType, "ghost"); err == nil || err.Kind != ErrRowInvisible {
		t.Fatalf("missing instance must be indistinguishable: %v", err)
	}
	if _, err := h.a.Write("nobody", empType, "secret", map[string]Value{"name": {Str: "Y"}}); err == nil || err.Kind != ErrRowInvisible {
		t.Fatalf("write to hidden instance must fail with same kind: %v", err)
	}
	if got := h.store.Version(empType, "secret"); got != 1 {
		t.Fatalf("rejected write must not bump version, got %d", got)
	}
}

func TestWriteErrorPrecedenceAndOrderIndependence(t *testing.T) {
	al, de := allowDeny()
	cfg := denyByDefault()
	cfg.WriteMode = WriteReject
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type: empType, ID: "e5",
		Values: map[string]Value{
			"name": {Str: "A"}, "salary": {Int: 1},
			"clearance": {Str: "low"}, "band": {Str: "low"},
		},
		Present: map[string]bool{"name": true, "salary": true,
			"clearance": true, "band": true},
	})
	mustRegRow(t, h.catalog, RowPolicy{
		ID: "row", ObjectType: empType, Effect: EffectAllow,
		Predicate: Predicate{Atoms: []Atom{{Property: "name", Op: OpNe, Str: "blocked"}}},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "w-name", ObjectType: empType,
		Property: "name", Write: al})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "w-salary", ObjectType: empType,
		Property: "salary", Write: de})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "w-band", ObjectType: empType,
		Property: "band", Write: al,
		Mask: func(Value) Value { return Value{Str: "nope"} }})

	_, errA := h.a.Write("u", empType, "e5", map[string]Value{"salary": {Int: 2}, "band": {Str: "low"}})
	_, errB := h.a.Write("u", empType, "e5", map[string]Value{"band": {Str: "low"}, "salary": {Int: 2}})
	if errA == nil || errA.Kind != ErrPropertyNotWritable || errB == nil || errB.Kind != ErrPropertyNotWritable {
		t.Fatalf("precedence not-writable must win in both orders: %v %v", errA, errB)
	}
	if errA.Property != "salary" {
		t.Fatalf("reported property = %q want salary", errA.Property)
	}
	_, err := h.a.Write("u", empType, "e5", map[string]Value{"band": {Str: "low"}})
	if err == nil || err.Kind != ErrMaskTypeViolation {
		t.Fatalf("want mask violation precedence 3, got %v", err)
	}
	if _, err := h.a.Write("u", empType, "e5", map[string]Value{"nope": {Str: "x"}}); err == nil || err.Kind != ErrUnknownProperty {
		t.Fatalf("want unknown property, got %v", err)
	}
	if h.store.Version(empType, "e5") != 1 {
		t.Fatal("rejected writes must not bump version")
	}
}

func TestWriteDropModeSilentlyDropsFields(t *testing.T) {
	al, de := allowDeny()
	cfg := Config{
		RowMode: AllowOverrides, PropertyMode: AllowOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectDeny, DefaultWrite: EffectDeny,
		WriteMode: WriteDrop,
	}
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type:    empType,
		ID:      "e6",
		Values:  map[string]Value{"name": {Str: "A"}, "salary": {Int: 10}},
		Present: map[string]bool{"name": true, "salary": true},
	})
	var clock int64 = 100
	h.store.SetClock(func() int64 { clock++; return clock })
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "w-name", ObjectType: empType,
		Property: "name", Write: al})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "d-salary", ObjectType: empType,
		Property: "salary", Write: de})

	res, err := h.a.Write("u", empType, "e6", map[string]Value{
		"salary": {Int: 999}, "name": {Str: "B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied["name"] || res.Applied["salary"] {
		t.Fatalf("applied = %+v", res.Applied)
	}
	if len(res.Dropped) != 1 || res.Dropped[0] != "salary" {
		t.Fatalf("dropped = %v", res.Dropped)
	}
	if res.Version != 2 {
		t.Fatalf("version = %d, want 2", res.Version)
	}
	raw, _ := h.store.GetRaw(empType, "e6")
	if raw.Values["salary"].Int != 10 || raw.Values["name"].Str != "B" {
		t.Fatalf("dropped field changed or accepted field missing: %+v", raw.Values)
	}
	if ts := h.store.LastWriteNanos(empType, "e6"); ts != 101 {
		t.Fatalf("last write = %d, want 101", ts)
	}
}

func TestConflictingPropertyPoliciesMergeDeterministically(t *testing.T) {
	al, de := allowDeny()
	for _, mode := range []MergeMode{DenyOverrides, AllowOverrides} {
		cfg := Config{
			RowMode: AllowOverrides, PropertyMode: mode,
			DefaultRow: EffectAllow, DefaultRead: EffectDeny, DefaultWrite: EffectDeny,
		}
		h := newHarness(t, cfg)
		h.store.Put(Instance{
			Type:    empType,
			ID:      "e7",
			Values:  map[string]Value{"name": {Str: "Z"}},
			Present: map[string]bool{"name": true},
		})
		h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "p-allow", ObjectType: empType,
			Property: "name", Read: al})
		h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "p-deny", ObjectType: empType,
			Property: "name", Read: de})
		view, err := h.a.Read("u", empType, "e7")
		if err != nil {
			t.Fatal(err)
		}
		_, present := view.Fields["name"]
		wantPresent := mode == AllowOverrides
		if present != wantPresent {
			t.Fatalf("mode %s: present=%v want %v", mode, present, wantPresent)
		}
	}
}

func TestWriteFieldOrderIndependence(t *testing.T) {
	al, de := allowDeny()
	cfg := Config{
		RowMode: AllowOverrides, PropertyMode: AllowOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectDeny, DefaultWrite: EffectDeny,
		WriteMode: WriteDrop,
	}
	h := newHarness(t, cfg)
	h.store.Put(Instance{
		Type:    empType,
		ID:      "o9",
		Values:  map[string]Value{"name": {Str: "n"}, "salary": {Int: 1}, "active": {Bool: true}},
		Present: map[string]bool{"name": true, "salary": true, "active": true},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "wn", ObjectType: empType,
		Property: "name", Write: al})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "ws", ObjectType: empType,
		Property: "salary", Write: de})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{ID: "wa", ObjectType: empType,
		Property: "active", Write: al})

	req := func() map[string]Value {
		return map[string]Value{
			"salary": {Int: 99},
			"active": {Bool: false},
			"name":   {Str: "new"},
		}
	}
	r1, e1 := h.a.Write("u", empType, "o9", req())
	if e1 != nil {
		t.Fatal(e1)
	}
	// Reset to a known state for the second ordering.
	h.store.Put(Instance{
		Type:    empType,
		ID:      "o9",
		Values:  map[string]Value{"name": {Str: "n"}, "salary": {Int: 1}, "active": {Bool: true}},
		Present: map[string]bool{"name": true, "salary": true, "active": true},
	})
	r2, e2 := h.a.Write("u", empType, "o9", req())
	if e2 != nil {
		t.Fatal(e2)
	}
	if !sameWriteResult(r1, r2) {
		t.Fatalf("write results depend on field order:\n%+v\n%+v", r1, r2)
	}
}
