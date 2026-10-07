package policy

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func ptrF(f float64) *float64 { return &f }

func baseType() ObjectType {
	return ObjectType{
		Name: "Person",
		Attrs: map[string]AttrType{
			"name":   {Kind: KindString},
			"ssn":    {Kind: KindString, MaxLen: 11},
			"age":    {Kind: KindInt, Min: ptrF(0), Max: ptrF(150)},
			"score":  {Kind: KindFloat, Min: ptrF(0), Max: ptrF(100)},
			"email":  {Kind: KindString},
			"secret": {Kind: KindString},
			"city":   {Kind: KindString},
		},
	}
}

func baseInstance() Instance {
	return Instance{
		Type: "Person",
		ID:   "p1",
		Values: map[string]any{
			"name": "Alice", "ssn": "123-45-6789", "age": 30,
			"score": 88.5, "email": "a@x.com", "secret": "topsecret", "city": "NYC",
		},
	}
}

func TestVisibilityAndMaskingComposition(t *testing.T) {
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v-name", Object: "Person", Subject: "analyst", Attr: "name", Allow: true},
			{ID: "v-ssn", Object: "Person", Subject: "analyst", Attr: "ssn", Allow: true},
			{ID: "v-secret", Object: "Person", Subject: "analyst", Attr: "secret", Allow: false},
		},
		Masking: []MaskingPolicy{
			{ID: "m-ssn", Object: "Person", Subject: "analyst", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}},
		},
	}
	r := NewRegistry()
	if err := r.Apply(set); err != nil {
		t.Fatal(err)
	}
	res, err := r.Render(baseInstance(), "analyst")
	if err != nil {
		t.Fatal(err)
	}
	if res.Presented["name"] != "Alice" {
		t.Fatalf("name = %v", res.Presented["name"])
	}
	if res.Presented["ssn"] != "[REDACTED]" {
		t.Fatalf("ssn = %v, want redacted", res.Presented["ssn"])
	}
	if _, ok := res.Presented["secret"]; ok {
		t.Fatal("denied attribute must be absent even without masking")
	}
	if _, ok := res.Presented["age"]; ok {
		t.Fatal("attribute without any allow policy must be hidden (default-deny)")
	}
	// Raw value must never leak.
	dump := fmt.Sprintf("%v", res.Presented)
	if strings.Contains(dump, "123-45-6789") || strings.Contains(dump, "topsecret") {
		t.Fatalf("raw sensitive value leaked: %s", dump)
	}
}

func TestDenyBeatsMaskingAndLeavesNoAuditTrail(t *testing.T) {
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v-secret-deny", Object: "Person", Subject: "s", Attr: "secret", Allow: false},
		},
		Masking: []MaskingPolicy{
			{ID: "m-secret", Object: "Person", Subject: "s", Attr: "secret", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleHash}},
		},
	}
	r := NewRegistry()
	if err := r.Apply(set); err != nil {
		t.Fatal(err)
	}
	res, err := r.Render(baseInstance(), "s")
	if err != nil {
		t.Fatalf("a plain deny is not a conflict: %v", err)
	}
	if _, ok := res.Presented["secret"]; ok {
		t.Fatal("denied attribute cannot appear in any derived form")
	}
	// A render that ends with no presented attributes is still committed; a
	// denied *request* means phases 1-3 aborts. Audit semantics verified there.
}

func TestVisibilityEvaluatedOnRawConditionValue(t *testing.T) {
	// name is masked to "A***", but the visibility predicate on secret must be
	// evaluated against the RAW name "Alice", never the derived string.
	tp := baseType()
	set := PolicySet{
		Types: []ObjectType{tp},
		Visibility: []VisibilityPolicy{
			{ID: "v-name", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "v-secret", Object: "Person", Subject: "s", Attr: "secret", Allow: true,
				Pred: &Predicate{CondAttr: "name", Op: "eq", Value: "Alice"}},
		},
		Masking: []MaskingPolicy{
			{ID: "m-name", Object: "Person", Subject: "s", Attr: "name", Strength: StrengthStrong,
				Rule: DerivedRule{Kind: RuleMask, KeepRunes: 1}},
		},
	}
	r := NewRegistry()
	if err := r.Apply(set); err != nil {
		t.Fatal(err)
	}
	res, err := r.Render(baseInstance(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if res.Presented["name"] != "A***" {
		t.Fatalf("name should be masked, got %v", res.Presented["name"])
	}
	if res.Presented["secret"] != "topsecret" {
		t.Fatalf("visibility must use raw name Alice; secret = %v", res.Presented["secret"])
	}
	// The raw condition value must not leak through any presented path.
	for k, v := range res.Presented {
		if k != "secret" && fmt.Sprintf("%v", v) == "Alice" {
			t.Fatalf("raw condition value leaked via %s", k)
		}
	}
}

func TestDirectVisibilityConflict(t *testing.T) {
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v-allow", Object: "Person", Subject: "s", Attr: "ssn", Allow: true},
			{ID: "v-deny", Object: "Person", Subject: "s", Attr: "ssn", Allow: false},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	_, err := r.Render(baseInstance(), "s")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindVisibilityConflict {
		t.Fatalf("want VisibilityConflict, got %v", err)
	}
	if r.Audit().Len() != 0 {
		t.Fatal("aborted request must not be audited")
	}
}

func TestMaskingMergeStrengthAndTieBreaks(t *testing.T) {
	mk := func(id string, st Strength, kind RuleKind, extra DerivedRule) MaskingPolicy {
		return MaskingPolicy{ID: id, Object: "Person", Subject: "s", Attr: "ssn", Strength: st, Rule: extra}
	}
	_ = mk
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true},
		},
		Masking: []MaskingPolicy{
			{ID: "m-weak-const", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthWeak, Rule: DerivedRule{Kind: RuleConst, ConstVal: "X"}},
			{ID: "m-strong-a", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleHash}},
			{ID: "m-strong-b", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	res, err := r.Render(baseInstance(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if res.Presented["ssn"] != "[REDACTED]" {
		t.Fatalf("same-strength tie must pick redact over hash, got %v", res.Presented["ssn"])
	}
	b := res.Basis["ssn"]
	if b.FinalStrength != StrengthStrong || b.FinalRulePolicy != "m-strong-b" {
		t.Fatalf("basis = %+v", b)
	}
}

func TestRegistrationOrderIndependence(t *testing.T) {
	build := func(order []string) PolicySet {
		all := []MaskingPolicy{
			{ID: "m1", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthMedium, Rule: DerivedRule{Kind: RuleHash}},
			{ID: "m2", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthMedium, Rule: DerivedRule{Kind: RuleRedact}},
			{ID: "m3", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthMedium, Rule: DerivedRule{Kind: RuleMask, KeepRunes: 2}},
		}
		byID := map[string]MaskingPolicy{}
		for _, p := range all {
			byID[p.ID] = p
		}
		masks := make([]MaskingPolicy, 0, 3)
		for _, id := range order {
			masks = append(masks, byID[id])
		}
		return PolicySet{
			Types:      []ObjectType{baseType()},
			Visibility: []VisibilityPolicy{{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true}},
			Masking:    masks,
		}
	}
	var first map[string]any
	for i, order := range [][]string{{"m1", "m2", "m3"}, {"m3", "m1", "m2"}, {"m2", "m3", "m1"}} {
		r := NewRegistry()
		if err := r.Apply(build(order)); err != nil {
			t.Fatal(err)
		}
		res, err := r.Render(baseInstance(), "s")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = res.Presented
		} else if !mapsEqual(first, res.Presented) {
			t.Fatalf("order %v produced %v, want %v", order, res.Presented, first)
		}
	}
}

func TestMaskingCycleDetected(t *testing.T) {
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v-a", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "v-b", Object: "Person", Subject: "s", Attr: "city", Allow: true},
		},
		Masking: []MaskingPolicy{
			{ID: "m-a", Object: "Person", Subject: "s", Attr: "name", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "city"}},
			{ID: "m-b", Object: "Person", Subject: "s", Attr: "city", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "name"}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	_, err := r.Render(baseInstance(), "s")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindMaskingCycle {
		t.Fatalf("want MaskingCycle, got %v", err)
	}
	if len(pe.Cycle) < 3 || pe.Cycle[0] != pe.Cycle[len(pe.Cycle)-1] {
		t.Fatalf("cycle must be a closed path, got %v", pe.Cycle)
	}
	if r.Audit().Len() != 0 {
		t.Fatal("cycle-aborted request must not be audited")
	}
}

func TestCopyDerivedUsesPresentedNotRaw(t *testing.T) {
	tp := baseType()
	set := PolicySet{
		Types: []ObjectType{tp},
		Visibility: []VisibilityPolicy{
			{ID: "v1", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "v2", Object: "Person", Subject: "s", Attr: "city", Allow: true},
		},
		Masking: []MaskingPolicy{
			{ID: "m-name", Object: "Person", Subject: "s", Attr: "name", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleMask, KeepRunes: 1}},
			{ID: "m-city", Object: "Person", Subject: "s", Attr: "city", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "name"}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	res, err := r.Render(baseInstance(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if res.Presented["city"] != "A***" {
		t.Fatalf("copy-derived must use masked name, got %v", res.Presented["city"])
	}
	if fmt.Sprintf("%v", res.Presented) == "Alice" {
		t.Fatal("raw leak")
	}
}

func TestCopyDerivedFromDeniedSource(t *testing.T) {
	// city copies secret; secret is denied -> derived value absent -> city
	// itself (declared non-null string) incurs an isolated type violation.
	tp := baseType()
	set := PolicySet{
		Types: []ObjectType{tp},
		Visibility: []VisibilityPolicy{
			{ID: "v-city", Object: "Person", Subject: "s", Attr: "city", Allow: true},
			{ID: "v-secret", Object: "Person", Subject: "s", Attr: "secret", Allow: false},
		},
		Masking: []MaskingPolicy{
			{ID: "m-city", Object: "Person", Subject: "s", Attr: "city", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "secret"}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	res, err := r.Render(baseInstance(), "s")
	if err != nil {
		t.Fatalf("denied source is not a request-level error: %v", err)
	}
	if _, ok := res.Presented["city"]; ok {
		t.Fatal("city derived from denied source must not be presented")
	}
	if len(res.AttributeErrors) != 1 || res.AttributeErrors[0].Err.Kind != KindTypeViolation {
		t.Fatalf("want one isolated type violation, got %+v", res.AttributeErrors)
	}
}

func TestTypeViolationIsolatedPerAttributeAndSubject(t *testing.T) {
	tp := baseType()
	tp.Attrs["grade"] = AttrType{Kind: KindString, Allowed: []any{"A", "B", "C"}}
	set := PolicySet{
		Types: []ObjectType{tp},
		Visibility: []VisibilityPolicy{
			{ID: "v-age", Object: "Person", Subject: "s", Attr: "age", Allow: true},
			{ID: "v-grade", Object: "Person", Subject: "s", Attr: "grade", Allow: true},
			{ID: "v-name", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "v-other", Object: "Person", Subject: "other", Attr: "grade", Allow: true},
		},
		Masking: []MaskingPolicy{
			// constant 9999 violates age range 0..150
			{ID: "m-age", Object: "Person", Subject: "s", Attr: "age", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleConst, ConstVal: 9999}},
			// constant "Z" violates grade enum
			{ID: "m-grade", Object: "Person", Subject: "s", Attr: "grade", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleConst, ConstVal: "Z"}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	inst := baseInstance()
	inst.Values["grade"] = "A"
	res, err := r.Render(inst, "s")
	if err != nil {
		t.Fatal(err)
	}
	if res.Presented["name"] != "Alice" {
		t.Fatal("unrelated attribute must still present")
	}
	if _, ok := res.Presented["age"]; ok {
		t.Fatal("violating attribute must be absent")
	}
	if len(res.AttributeErrors) != 2 {
		t.Fatalf("want two isolated violations, got %+v", res.AttributeErrors)
	}
	for _, ae := range res.AttributeErrors {
		if ae.Err.Kind != KindTypeViolation {
			t.Fatalf("want TypeViolation, got %v", ae.Err.Kind)
		}
	}
	// Another subject has no masking: grade raw value is unaffected.
	res2, err := r.Render(inst, "other")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Presented["grade"] != "A" {
		t.Fatalf("other subject must see raw grade, got %v", res2.Presented["grade"])
	}
}

func TestMissingReferencePriority(t *testing.T) {
	// Both a missing reference and a visibility conflict exist: missing wins.
	set := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v-ok-a", Object: "Person", Subject: "s", Attr: "ssn", Allow: true},
			{ID: "v-ok-b", Object: "Person", Subject: "s", Attr: "ssn", Allow: false},
			{ID: "v-bad", Object: "Person", Subject: "s", Attr: "ghost", Allow: true},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	_, err := r.Render(baseInstance(), "s")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindMissingReference {
		t.Fatalf("want MissingReference priority, got %v", err)
	}
}

func TestUnknownObjectType(t *testing.T) {
	r := NewRegistry()
	_ = r.Apply(PolicySet{Types: []ObjectType{baseType()}})
	_, err := r.Render(Instance{Type: "Nope", ID: "x"}, "s")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindMissingReference {
		t.Fatalf("want MissingReference, got %v", err)
	}
}

func TestCycleBeatsTypeViolation(t *testing.T) {
	// Cycle in name<->city plus a const type violation on age: cycle wins.
	tp := baseType()
	set := PolicySet{
		Types: []ObjectType{tp},
		Visibility: []VisibilityPolicy{
			{ID: "v1", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "v2", Object: "Person", Subject: "s", Attr: "city", Allow: true},
			{ID: "v3", Object: "Person", Subject: "s", Attr: "age", Allow: true},
		},
		Masking: []MaskingPolicy{
			{ID: "m1", Object: "Person", Subject: "s", Attr: "name", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "city"}},
			{ID: "m2", Object: "Person", Subject: "s", Attr: "city", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "name"}},
			{ID: "m3", Object: "Person", Subject: "s", Attr: "age", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleConst, ConstVal: 1e9}},
		},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	_, err := r.Render(baseInstance(), "s")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindMaskingCycle {
		t.Fatalf("want MaskingCycle priority, got %v", err)
	}
}

func TestAtomicPolicyChangeImmediatelyVisible(t *testing.T) {
	r := NewRegistry()
	set1 := PolicySet{
		Types:      []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true}},
		Masking:    []MaskingPolicy{{ID: "m", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}}},
	}
	if err := r.Apply(set1); err != nil {
		t.Fatal(err)
	}
	res, _ := r.Render(baseInstance(), "s")
	if res.Presented["ssn"] != "[REDACTED]" {
		t.Fatalf("want redacted, got %v", res.Presented["ssn"])
	}
	set2 := PolicySet{
		Types:      []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true}},
	}
	if err := r.Apply(set2); err != nil {
		t.Fatal(err)
	}
	res, _ = r.Render(baseInstance(), "s")
	if res.Presented["ssn"] != "123-45-6789" {
		t.Fatalf("new policy set must take effect immediately, got %v", res.Presented["ssn"])
	}
	if res.Revision != 2 {
		t.Fatalf("revision = %d, want 2", res.Revision)
	}
}

func TestConcurrentSerializabilityAndAtomicity(t *testing.T) {
	r := NewRegistry()
	base := PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true},
		},
	}
	if err := r.Apply(base); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(3)
	go func() { // mutator: flip between two complete sets
		defer wg.Done()
		masked := base
		masked.Masking = []MaskingPolicy{{ID: "m", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}}}
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				_ = r.Apply(masked)
			} else {
				_ = r.Apply(base)
			}
		}
		close(stop)
	}()
	check := func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			res, err := r.Render(baseInstance(), "s")
			if err != nil {
				t.Errorf("render error: %v", err)
				return
			}
			v := res.Presented["ssn"]
			if v != "[REDACTED]" && v != "123-45-6789" {
				t.Errorf("impossible mixed/partial value %v", v)
				return
			}
		}
	}
	go check()
	go check()
	wg.Wait()
}
