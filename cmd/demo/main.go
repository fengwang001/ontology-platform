// Command demo exercises the masking/visibility arbitration module end to end:
// it registers policies, renders an instance to two subjects, mutates the
// policy set atomically, and prints the audit log as JSON.
package main

import (
	"fmt"

	"ontology/policy"
)

func ptrF(f float64) *float64 { return &f }

func main() {
	person := policy.ObjectType{
		Name: "Person",
		Attrs: map[string]policy.AttrType{
			"name":   {Kind: policy.KindString},
			"ssn":    {Kind: policy.KindString, MaxLen: 11},
			"age":    {Kind: policy.KindInt, Min: ptrF(0), Max: ptrF(150)},
			"secret": {Kind: policy.KindString},
		},
	}
	inst := policy.Instance{
		Type:   "Person",
		ID:     "person-42",
		Values: map[string]any{"name": "Alice", "ssn": "123-45-6789", "age": 30, "secret": "project-x"},
	}

	reg := policy.NewRegistry()
	must(reg.Apply(policy.PolicySet{
		Types: []policy.ObjectType{person},
		Visibility: []policy.VisibilityPolicy{
			{ID: "vis-name-analyst", Object: "Person", Subject: "analyst", Attr: "name", Allow: true},
			{ID: "vis-ssn-analyst", Object: "Person", Subject: "analyst", Attr: "ssn", Allow: true},
			{ID: "vis-age-analyst", Object: "Person", Subject: "analyst", Attr: "age", Allow: true},
			{ID: "vis-name-auditor", Object: "Person", Subject: "auditor", Attr: "name", Allow: true},
			{ID: "vis-secret-auditor", Object: "Person", Subject: "auditor", Attr: "secret", Allow: true},
		},
		Masking: []policy.MaskingPolicy{
			{ID: "msk-ssn", Object: "Person", Subject: "analyst", Attr: "ssn",
				Strength: policy.StrengthStrong, Rule: policy.DerivedRule{Kind: policy.RuleRedact}},
			{ID: "msk-name", Object: "Person", Subject: "auditor", Attr: "name",
				Strength: policy.StrengthWeak, Rule: policy.DerivedRule{Kind: policy.RuleMask, KeepRunes: 1}},
		},
	}))

	for _, subject := range []string{"analyst", "auditor"} {
		res, err := reg.Render(inst, subject)
		must(err)
		fmt.Printf("subject=%s revision=%d presented=%v attrErrors=%v\n",
			subject, res.Revision, res.Presented, res.AttributeErrors)
	}

	// Atomic change: remove the ssn masking.
	must(reg.Apply(policy.PolicySet{
		Types: []policy.ObjectType{person},
		Visibility: []policy.VisibilityPolicy{
			{ID: "vis-name-analyst", Object: "Person", Subject: "analyst", Attr: "name", Allow: true},
			{ID: "vis-ssn-analyst", Object: "Person", Subject: "analyst", Attr: "ssn", Allow: true},
			{ID: "vis-age-analyst", Object: "Person", Subject: "analyst", Attr: "age", Allow: true},
		},
	}))
	res, err := reg.Render(inst, "analyst")
	must(err)
	fmt.Printf("after change subject=analyst revision=%d presented=%v\n", res.Revision, res.Presented)

	raw, err := reg.Audit().JSON()
	must(err)
	fmt.Printf("audit entries=%d\n%s\n", reg.Audit().Len(), string(raw))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
