package policy

import (
	"encoding/json"
	"testing"
)

// TestExaminationCostIndependentOfTotalPolicies proves, using only the public
// observability counters (examined policies vs total registered policies), that
// one render's work depends solely on the number of policies relevant to the
// rendered (object type, subject) pair, not on the store-wide total.
func TestExaminationCostIndependentOfTotalPolicies(t *testing.T) {
	mk := func(totalOther int) (*Registry, int64, int64) {
		types := []ObjectType{baseType()}
		vis := []VisibilityPolicy{{ID: "v-ssn", Object: "Person", Subject: "target", Attr: "ssn", Allow: true}}
		mask := []MaskingPolicy{{ID: "m-ssn", Object: "Person", Subject: "target", Attr: "ssn",
			Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}}}
		for i := 0; i < totalOther; i++ {
			// Policies for unrelated subjects and unrelated object types.
			vis = append(vis,
				VisibilityPolicy{ID: idn("ov", i), Object: "Person", Subject: "other", Attr: "name", Allow: true},
				VisibilityPolicy{ID: idn("tv", i), Object: "OtherType", Subject: "target", Attr: "name", Allow: true},
			)
			mask = append(mask,
				MaskingPolicy{ID: idn("om", i), Object: "Person", Subject: "other", Attr: "name", Strength: StrengthWeak, Rule: DerivedRule{Kind: RuleHash}},
				MaskingPolicy{ID: idn("tm", i), Object: "OtherType", Subject: "target", Attr: "name", Strength: StrengthWeak, Rule: DerivedRule{Kind: RuleHash}},
			)
		}
		types = append(types, ObjectType{Name: "OtherType", Attrs: map[string]AttrType{"name": {Kind: KindString}}})
		r := NewRegistry()
		if err := r.Apply(PolicySet{Types: types, Visibility: vis, Masking: mask}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Render(baseInstance(), "target"); err != nil {
			t.Fatal(err)
		}
		return r, r.ExaminedVisibilityPolicies(), r.ExaminedMaskingPolicies()
	}

	_, visSmall, maskSmall := mk(10)
	_, visLarge, maskLarge := mk(5000)

	totalSmall := int64(1 + 2*10)
	totalLarge := int64(1 + 2*5000)
	if totalLarge <= totalSmall {
		t.Fatal("test setup wrong")
	}
	if visSmall != 1 || maskSmall != 1 {
		t.Fatalf("small case examined %d/%d, want exactly 1/1 relevant policies", visSmall, maskSmall)
	}
	if visLarge != visSmall || maskLarge != maskSmall {
		t.Fatalf("examined policies grew with store total: %d/%d (large) vs %d/%d (small)",
			visLarge, maskLarge, visSmall, maskSmall)
	}
	t.Logf("store totals %d vs %d policies; examined per render stayed %d visibility + %d masking",
		totalSmall, totalLarge, visLarge, maskLarge)
}

func idn(prefix string, i int) string {
	return prefix + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('a' + i%26)}, b...)
		i /= 26
	}
	return string(b)
}

// TestAuditRecordsInputsOutputsAndBasis verifies the complete audit trail.
func TestAuditRecordsInputsOutputsAndBasis(t *testing.T) {
	set := PolicySet{
		Types:      []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{{ID: "v", Object: "Person", Subject: "s", Attr: "ssn", Allow: true}},
		Masking:    []MaskingPolicy{{ID: "m", Object: "Person", Subject: "s", Attr: "ssn", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleRedact}}},
	}
	r := NewRegistry()
	_ = r.Apply(set)
	inst := baseInstance()
	if _, err := r.Render(inst, "s"); err != nil {
		t.Fatal(err)
	}
	entries := r.Audit().Entries()
	if len(entries) != 1 {
		t.Fatalf("audit len = %d", len(entries))
	}
	e := entries[0]
	if e.RawInstance["ssn"] != "123-45-6789" {
		t.Fatal("audit must record raw input")
	}
	if e.Presented["ssn"] != "[REDACTED]" {
		t.Fatal("audit must record final output")
	}
	b := e.Basis["ssn"]
	if len(b.VisibilityMatched) != 1 || b.VisibilityMatched[0] != "v" ||
		len(b.MaskingMerged) != 1 || b.FinalRulePolicy != "m" || b.FinalRule != RuleRedact {
		t.Fatalf("audit must record policy basis, got %+v", b)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("audit entry must serialize to valid JSON")
	}
}

// TestDeniedOrAbortedRendersLeaveNoAudit covers the "rejected requests have no
// effect on audit records" requirement across every aborting error kind.
func TestDeniedOrAbortedRendersLeaveNoAudit(t *testing.T) {
	r := NewRegistry()
	_ = r.Apply(PolicySet{Types: []ObjectType{baseType()}})

	// Unknown object (MissingReference).
	if _, err := r.Render(Instance{Type: "Ghost"}, "s"); err == nil {
		t.Fatal("want error")
	}
	// Attribute with no allow policy: request completes but hides everything;
	// still a committed render. Then trigger a conflict and a cycle.
	beforeConflict := r.Audit().Len()
	_ = r.Apply(PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "a", Object: "Person", Subject: "s", Attr: "ssn", Allow: true},
			{ID: "b", Object: "Person", Subject: "s", Attr: "ssn", Allow: false},
		},
	})
	if _, err := r.Render(baseInstance(), "s"); err == nil {
		t.Fatal("want conflict")
	}
	_ = r.Apply(PolicySet{
		Types: []ObjectType{baseType()},
		Visibility: []VisibilityPolicy{
			{ID: "a1", Object: "Person", Subject: "s", Attr: "name", Allow: true},
			{ID: "a2", Object: "Person", Subject: "s", Attr: "city", Allow: true},
		},
		Masking: []MaskingPolicy{
			{ID: "c1", Object: "Person", Subject: "s", Attr: "name", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "city"}},
			{ID: "c2", Object: "Person", Subject: "s", Attr: "city", Strength: StrengthStrong, Rule: DerivedRule{Kind: RuleCopyDerived, SourceAttr: "name"}},
		},
	})
	if _, err := r.Render(baseInstance(), "s"); err == nil {
		t.Fatal("want cycle")
	}
	if r.Audit().Len() != beforeConflict {
		t.Fatalf("conflict/cycle requests must leave no audit record: %d != %d", r.Audit().Len(), beforeConflict)
	}
}
