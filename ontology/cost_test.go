package ontology

import (
	"fmt"
	"testing"
)

// TestDecisionCostIndependentOfTenantCount 是可观测的开销证明：
// 对同一归属租户的同一实例的判定，其考察的规则条目数
// （Decision.Basis.OverridesExamined / DefaultsExamined）
// 不随系统租户总数或其他租户的覆盖规则数量增长。
func TestDecisionCostIndependentOfTenantCount(t *testing.T) {
	for _, tenantCount := range []int{1, 10, 100, 1000} {
		t.Run(fmt.Sprintf("tenants=%d", tenantCount), func(t *testing.T) {
			e := NewEngine()
			if err := e.RegisterObjectType(ObjectTypeDef{
				Name:       "doc",
				Attributes: []string{"title", "secret"},
			}); err != nil {
				t.Fatalf("register type: %v", err)
			}
			if err := e.SetGlobalDefault("doc", []RuleEntry{
				{Action: "read", Effect: Allow},
				{Action: "read", Attribute: "secret", Effect: Deny},
			}); err != nil {
				t.Fatalf("set default: %v", err)
			}
			for i := 0; i < tenantCount; i++ {
				tenant := fmt.Sprintf("t%d", i)
				if err := e.RegisterTenant(tenant); err != nil {
					t.Fatalf("register tenant: %v", err)
				}
				// 归属租户 t0 登记 3 条覆盖；其他租户各登记 5 条覆盖。
				n := 5
				if i == 0 {
					n = 3
				}
				var entries []RuleEntry
				for j := 0; j < n; j++ {
					entries = append(entries, RuleEntry{
						Action: Action(fmt.Sprintf("a%d", j)), Effect: Deny,
					})
				}
				if err := e.SetTenantOverride(tenant, "doc", entries); err != nil {
					t.Fatalf("set override: %v", err)
				}
				if err := e.RegisterInstance(Instance{
					ID: tenant + "-inst", Type: "doc", OwnerTenant: tenant,
				}); err != nil {
					t.Fatalf("register instance: %v", err)
				}
			}

			d, err := e.Decide(Subject{Tenant: "t999"}, "read", "t0-inst", "title")
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if d.Basis.OverridesExamined != 3 {
				t.Fatalf("tenants=%d: overrides examined = %d, want 3 (owner tenant only)",
					tenantCount, d.Basis.OverridesExamined)
			}
			if d.Basis.DefaultsExamined != 2 {
				t.Fatalf("tenants=%d: defaults examined = %d, want 2",
					tenantCount, d.Basis.DefaultsExamined)
			}
			if d.Effect != Allow {
				t.Fatalf("tenants=%d: effect = %v, want allow", tenantCount, d.Effect)
			}

			// 归属租户有 5 条覆盖的实例：考察数恒为 5，与租户总数无关。
			last := fmt.Sprintf("t%d-inst", tenantCount-1)
			d, err = e.Decide(Subject{Tenant: "t0"}, "read", last, "title")
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			want := 3
			if tenantCount > 1 {
				want = 5
			}
			if d.Basis.OverridesExamined != want {
				t.Fatalf("tenants=%d: overrides examined = %d, want %d",
					tenantCount, d.Basis.OverridesExamined, want)
			}
		})
	}
}
