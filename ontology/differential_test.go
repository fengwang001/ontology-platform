package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// engineAPI 是主实现与朴素参照实现共同满足的接口。
type engineAPI interface {
	RegisterTenant(id string) error
	RegisterObjectType(def ObjectTypeDef) error
	RegisterInstance(inst Instance) error
	SetGlobalDefault(typeName string, entries []RuleEntry) error
	SetTenantOverride(tenant, typeName string, entries []RuleEntry) error
	RevokeTenantOverride(tenant, typeName string) error
	Decide(subject Subject, action Action, instanceID, attribute string) (Decision, error)
}

var (
	diffTenants    = []string{"t0", "t1", "t2", "t3", "t4", "t5"}
	diffTypes      = []string{"doc", "open"}
	diffAttributes = []string{"title", "secret"}
	diffRoles      = []string{"", "admin", "analyst"}
	diffActions    = []Action{"read", "write"}
	diffPredicates = []string{"", "highValue", "sameTenant", "ghost"}
	diffBasises    = []string{"", "", "dpo-1", "legal-9"}
)

func diffTypeDefs() []ObjectTypeDef {
	preds := map[string]PredicateFunc{
		"highValue":  func(inst Instance, _ Subject) bool { return inst.Attributes["level"] == "high" },
		"sameTenant": func(inst Instance, subj Subject) bool { return inst.OwnerTenant == subj.Tenant },
	}
	return []ObjectTypeDef{
		{Name: "doc", Attributes: diffAttributes, Predicates: preds, RequireRelaxationBasis: true},
		{Name: "open", Attributes: diffAttributes, Predicates: preds, RequireRelaxationBasis: false},
	}
}

func pickString(r *rand.Rand, xs []string) string { return xs[r.Intn(len(xs))] }

func randEntry(r *rand.Rand) RuleEntry {
	effect := Deny
	if r.Intn(2) == 0 {
		effect = Allow
	}
	return RuleEntry{
		Role:      diffRoles[r.Intn(len(diffRoles))],
		Action:    diffActions[r.Intn(len(diffActions))],
		Attribute: pickString(r, append([]string{""}, diffAttributes...)),
		Predicate: diffPredicates[r.Intn(len(diffPredicates))],
		Effect:    effect,
		Basis:     diffBasises[r.Intn(len(diffBasises))],
	}
}

func randEntries(r *rand.Rand) []RuleEntry {
	n := r.Intn(5)
	out := make([]RuleEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, randEntry(r))
	}
	return out
}

func randSubject(r *rand.Rand) Subject {
	roles := []string{}
	for _, role := range []string{"admin", "analyst"} {
		if r.Intn(2) == 0 {
			roles = append(roles, role)
		}
	}
	return Subject{
		ID:     fmt.Sprintf("u%d", r.Intn(4)),
		Tenant: pickString(r, diffTenants),
		Roles:  roles,
	}
}

// setupDiff 在所有引擎上注册相同的租户、对象类型与实例池，返回实例池。
func setupDiff(t *testing.T, engines ...engineAPI) []Instance {
	t.Helper()
	for _, tenant := range diffTenants {
		for _, e := range engines {
			if err := e.RegisterTenant(tenant); err != nil {
				t.Fatalf("register tenant: %v", err)
			}
		}
	}
	for _, def := range diffTypeDefs() {
		for _, e := range engines {
			if err := e.RegisterObjectType(def); err != nil {
				t.Fatalf("register type: %v", err)
			}
		}
	}
	var instances []Instance
	for i, tenant := range diffTenants {
		for _, typeName := range diffTypes {
			level := "low"
			if i%2 == 0 {
				level = "high"
			}
			inst := Instance{
				ID:          tenant + "-" + typeName,
				Type:        typeName,
				OwnerTenant: tenant,
				Attributes:  map[string]string{"level": level},
			}
			for _, e := range engines {
				if err := e.RegisterInstance(inst); err != nil {
					t.Fatalf("register instance: %v", err)
				}
			}
			instances = append(instances, inst)
		}
	}
	return instances
}

func assertSameError(t *testing.T, seed int64, op int, what string, errMain, errRef error) {
	t.Helper()
	if (errMain == nil) != (errRef == nil) {
		t.Fatalf("seed=%d op=%d %s: nil mismatch main=%v ref=%v", seed, op, what, errMain, errRef)
	}
	if errMain != nil && ClassOf(errMain) != ClassOf(errRef) {
		t.Fatalf("seed=%d op=%d %s: class mismatch main=%v ref=%v", seed, op, what, errMain, errRef)
	}
}

// TestDifferentialRandomSequences 在大量随机生成的租户与规则变更序列上，
// 将主实现与独立维护的朴素参照实现逐项对照。
func TestDifferentialRandomSequences(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			main := NewEngine()
			ref := NewReferenceEngine()
			instances := setupDiff(t, main, ref)

			for op := 0; op < 800; op++ {
				kind := r.Intn(100)
				switch {
				case kind < 20: // 全局默认规则变更
					typeName := pickString(r, diffTypes)
					entries := randEntries(r)
					errMain := main.SetGlobalDefault(typeName, entries)
					errRef := ref.SetGlobalDefault(typeName, entries)
					assertSameError(t, seed, op, "set-default", errMain, errRef)
				case kind < 45: // 租户覆盖规则变更
					tenant := pickString(r, diffTenants)
					typeName := pickString(r, diffTypes)
					entries := randEntries(r)
					errMain := main.SetTenantOverride(tenant, typeName, entries)
					errRef := ref.SetTenantOverride(tenant, typeName, entries)
					assertSameError(t, seed, op, "set-override", errMain, errRef)
				case kind < 55: // 撤销租户覆盖
					tenant := pickString(r, diffTenants)
					typeName := pickString(r, diffTypes)
					errMain := main.RevokeTenantOverride(tenant, typeName)
					errRef := ref.RevokeTenantOverride(tenant, typeName)
					assertSameError(t, seed, op, "revoke", errMain, errRef)
				case kind < 62: // 登记新实例
					inst := Instance{
						ID:          fmt.Sprintf("x-%d-%d", seed, op),
						Type:        pickString(r, diffTypes),
						OwnerTenant: pickString(r, diffTenants),
						Attributes:  map[string]string{"level": pickString(r, []string{"high", "low"})},
					}
					errMain := main.RegisterInstance(inst)
					errRef := ref.RegisterInstance(inst)
					assertSameError(t, seed, op, "register-instance", errMain, errRef)
					if errMain == nil {
						instances = append(instances, inst)
					}
				default: // 访问判定
					inst := instances[r.Intn(len(instances))]
					subj := randSubject(r)
					action := diffActions[r.Intn(len(diffActions))]
					attribute := pickString(r, []string{"title", "secret", "nope"})
					dMain, errMain := main.Decide(subj, action, inst.ID, attribute)
					dRef, errRef := ref.Decide(subj, action, inst.ID, attribute)
					assertSameError(t, seed, op, "decide", errMain, errRef)
					if errMain == nil && !reflect.DeepEqual(dMain, dRef) {
						t.Fatalf("seed=%d op=%d decide(%+v %s %s %s): decision mismatch\nmain=%+v\nref =%+v",
							seed, op, subj, action, inst.ID, attribute, dMain, dRef)
					}
				}
			}
		})
	}
}
