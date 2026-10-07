package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

func ptr(f float64) *float64 { return &f }

// Example shows the full read/write adjudication loop.
func Example() {
	catalog := ontology.NewPolicyCatalog()
	store := ontology.NewStore()
	audit := ontology.NewAuditLogger(nil)

	cfg := ontology.Config{
		RowMode:      ontology.DenyOverrides,
		PropertyMode: ontology.DenyOverrides,
		DefaultRow:   ontology.EffectDeny,
		DefaultRead:  ontology.EffectDeny,
		DefaultWrite: ontology.EffectDeny,
		WriteMode:    ontology.WriteReject,
	}
	adj := ontology.NewAdjudicator(cfg, catalog, store, audit)

	adj.RegisterType(&ontology.ObjectType{
		Name: "Employee",
		Properties: []ontology.PropertyDef{
			{Name: "name", Type: ontology.DeclaredType{Kind: ontology.KindString}},
			{Name: "salary", Type: ontology.DeclaredType{Kind: ontology.KindInt, Min: ptr(0)}},
		},
	})

	allow := ontology.EffectAllow
	deny := ontology.EffectDeny

	// Row visible only to managers when salary >= 1000 (raw value).
	_ = catalog.RegisterRowPolicy(ontology.RowPolicy{
		ID:         "manager-row",
		ObjectType: "Employee",
		Subjects:   []string{"manager"},
		Effect:     ontology.EffectAllow,
		Predicate: ontology.Predicate{Atoms: []ontology.Atom{
			{Property: "salary", Op: ontology.OpGe, Numeric: 1000},
		}},
	})
	// Everyone may read name; nobody may read salary; managers may write name.
	_ = catalog.RegisterPropertyPolicy(ontology.PropertyPolicy{
		ID: "read-name", ObjectType: "Employee", Property: "name", Read: &allow,
	})
	_ = catalog.RegisterPropertyPolicy(ontology.PropertyPolicy{
		ID: "deny-salary", ObjectType: "Employee", Property: "salary", Read: &deny,
	})
	_ = catalog.RegisterPropertyPolicy(ontology.PropertyPolicy{
		ID: "write-name", ObjectType: "Employee", Subjects: []string{"manager"},
		Property: "name", Write: &allow,
	})

	store.Put(ontology.Instance{
		Type: "Employee", ID: "e1",
		Values: map[string]ontology.Value{
			"name":   {Str: "Ada"},
			"salary": {Int: 1200},
		},
	})

	view, err := adj.Read("manager", "Employee", "e1")
	fmt.Println("read err:", err)
	fmt.Println("name:", view.Fields["name"].Value.Str)
	_, salaryPresent := view.Fields["salary"]
	fmt.Println("salary field present:", salaryPresent)
	fmt.Println("salary status:", view.Status["salary"])

	_, err = adj.Write("manager", "Employee", "e1",
		map[string]ontology.Value{"name": {Str: "Ada Lovelace"}})
	fmt.Println("write err:", err)

	_, err = adj.Write("intern", "Employee", "e1",
		map[string]ontology.Value{"name": {Str: "x"}})
	if err != nil {
		fmt.Println("intern write:", err.Kind)
	}
	// Output:
	// read err: <nil>
	// name: Ada
	// salary field present: false
	// salary status: absent_unreadable
	// write err: <nil>
	// intern write: row_invisible
}
