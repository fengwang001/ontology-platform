package binding_test

import (
	"fmt"

	"ontology/ontology/binding"
)

func strVals(vals ...string) []binding.Value {
	out := make([]binding.Value, len(vals))
	for i, v := range vals {
		out[i] = binding.Value{Raw: v}
	}
	return out
}

func Example() {
	store := binding.NewMemoryInstanceStore()
	registry := binding.NewRegistry(store)

	registry.DeclareField(binding.FieldDef{
		ObjectType: "Order", Name: "statusCode",
		Type:    binding.FieldType{Kind: binding.KindString},
		Allowed: strVals("paid", "void"),
	})
	registry.DeclareField(binding.FieldDef{
		ObjectType: "Ledger", Name: "entryKind",
		Type:    binding.FieldType{Kind: binding.KindString},
		Allowed: strVals("INCOME", "NONE"),
	})

	spec := binding.BindingSpec{
		LinkType:   "OrderLedger",
		LeftObject: "Order", LeftField: "statusCode",
		RightObject: "Ledger", RightField: "entryKind",
		Direction: binding.Bidirectional,
		Correspondence: binding.Correspondence{
			Mapping: []binding.ValuePair{
				{Left: binding.Value{Raw: "paid"}, Right: binding.Value{Raw: "INCOME"}},
				{Left: binding.Value{Raw: "void"}, Right: binding.Value{Raw: "NONE"}},
			},
			Missing: binding.MissingPolicy{MapMissing: true},
		},
	}
	if err := registry.DeclareBinding(spec); err != nil {
		fmt.Println("declare rejected:", err)
		return
	}

	res, _ := registry.Query("OrderLedger", binding.QLeftToRight)
	fmt.Println(res.Verdict)

	// 右侧新增一个没有前像的取值：双向对应被破坏。
	registry.UpdateField(binding.FieldDef{
		ObjectType: "Ledger", Name: "entryKind",
		Type:    binding.FieldType{Kind: binding.KindString},
		Allowed: strVals("INCOME", "NONE", "PENDING"),
	})
	res2, _ := registry.Query("OrderLedger", binding.QLeftToRight)
	fmt.Println(res2.Verdict)

	// 每次核验都留有审计记录。
	fmt.Println("audit entries:", len(registry.AuditLog()))
	// Output:
	// compatible
	// one_way_only
	// audit entries: 3
}
