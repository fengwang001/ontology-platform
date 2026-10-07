package main

import (
	"fmt"

	"ontology/ontology"
)

// 演示：多租户命名空间权限继承覆盖模块的端到端流程。
func main() {
	e := ontology.NewEngine()

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(e.RegisterTenant("tenant-a"))
	must(e.RegisterTenant("tenant-b"))
	must(e.RegisterObjectType(ontology.ObjectTypeDef{
		Name:       "contract",
		Attributes: []string{"title", "amount"},
		Predicates: map[string]ontology.PredicateFunc{
			"highValue": func(inst ontology.Instance, _ ontology.Subject) bool {
				return inst.Attributes["level"] == "high"
			},
		},
		RequireRelaxationBasis: true,
	}))
	must(e.RegisterInstance(ontology.Instance{
		ID: "c-1", Type: "contract", OwnerTenant: "tenant-a",
		Attributes: map[string]string{"level": "high"},
	}))

	// 全局默认规则：title 可读，amount 拒绝。
	must(e.SetGlobalDefault("contract", []ontology.RuleEntry{
		{Action: "read", Attribute: "title", Effect: ontology.Allow},
		{Action: "read", Attribute: "amount", Effect: ontology.Deny},
	}))
	// tenant-a 放宽 amount（带授权依据）。
	must(e.SetTenantOverride("tenant-a", "contract", []ontology.RuleEntry{
		{Action: "read", Attribute: "amount", Effect: ontology.Allow, Basis: "dpo-approval-42"},
	}))
	// tenant-b 未经依据放宽：被拒绝，不影响任何状态。
	if err := e.SetTenantOverride("tenant-b", "contract", []ontology.RuleEntry{
		{Action: "read", Attribute: "amount", Effect: ontology.Allow},
	}); err != nil {
		fmt.Printf("tenant-b relax rejected as expected: %v\n", err)
	}

	// tenant-b 的主体跨租户访问 tenant-a 的实例：按实例归属租户（tenant-a）的覆盖判定。
	subj := ontology.Subject{ID: "u-9", Tenant: "tenant-b"}
	for _, attr := range []string{"title", "amount"} {
		d, err := e.Decide(subj, "read", "c-1", attr)
		if err != nil {
			panic(err)
		}
		fmt.Printf("decide tenant-b -> c-1.%s: %s (owner=%s overrideVersion=%d winning=%v)\n",
			attr, d.Effect, d.Basis.OwnerTenant, d.Basis.OverrideVersion, d.Basis.WinningEntries)
	}

	fmt.Printf("audit records: %d\n", len(e.Audit()))
}
