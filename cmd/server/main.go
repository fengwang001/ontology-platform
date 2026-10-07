package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()
	s.AddTenant("tenant-a")
	s.AddTenant("tenant-b")
	s.DefineObjectType(ontology.ObjectTypeDef{
		Name:  "Document",
		Attrs: []string{"title", "body", "secret"},
		Predicates: []ontology.Predicate{
			{ID: "own-dept", Field: "dept", Op: "eq", Value: "eng"},
		},
	})
	must(s.SetLooseningBasisRequired("Document", true))
	must(s.PutDefault("Document", "reader", []ontology.Statement{
		{ID: "d1", Scope: ontology.Scope{Kind: ontology.ScopeAttrSet, Members: []string{"title", "body"}}, Effect: ontology.Allow},
		{ID: "d2", Scope: ontology.Scope{Kind: ontology.ScopeAttr, Name: "secret"}, Effect: ontology.Deny},
		{ID: "d3", Scope: ontology.Scope{Kind: ontology.ScopePredicate, Name: "own-dept"}, Effect: ontology.Allow},
	}))
	// tenant-a 放宽 secret（携带授权依据），tenant-b 不做任何覆盖。
	must(s.PutOverride("tenant-a", "Document", "reader", []ontology.Statement{
		{ID: "o1", Scope: ontology.Scope{Kind: ontology.ScopeAttr, Name: "secret"}, Effect: ontology.Allow, Basis: "legal-hold-2026"},
	}))

	// tenant-b 的主体跨租户访问 tenant-a 的实例：适用实例归属租户 tenant-a 的覆盖。
	dec, err := s.Decide(ontology.AccessRequest{
		SubjectTenant:  "tenant-b",
		SubjectClass:   "reader",
		Type:           "Document",
		InstanceTenant: "tenant-a",
		Instance:       ontology.Instance{ID: "doc-1", Attrs: map[string]any{"dept": "eng"}},
	})
	must(err)
	fmt.Printf("row=%v attrs=%v\n", dec.RowAllowed, dec.Attrs)
	fmt.Printf("basis=%v\n", dec.Trace.Basis)
	fmt.Printf("examined: default=%d override=%d\n",
		dec.Trace.DefaultEntriesExamined, dec.Trace.OverrideEntriesExamined)

	fmt.Println("--- audit ---")
	for _, rec := range s.Audit() {
		fmt.Printf("#%d %s %s -> %s\n", rec.Seq, rec.Op, rec.Input, rec.Output)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
