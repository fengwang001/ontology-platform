package fedalloc

import (
	"math/big"
	"sort"
)

// buildPlan derives the change plan and total migration from targets.
func buildPlan(views []clusterView, targets []int64, total int64) AllocationResult {
	result := AllocationResult{
		Total:   total,
		Targets: make([]Target, 0, len(views)),
		Plan:    []Change{},
	}

	order := make([]int, len(views))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return views[order[a]].name < views[order[b]].name
	})

	sum := new(big.Int)
	migration := new(big.Int)
	for _, i := range order {
		v := &views[i]
		t := targets[i]
		sum.Add(sum, big.NewInt(t))
		result.Targets = append(result.Targets, Target{Name: v.name, Replicas: t})
		if t != v.currentReplicas {
			result.Plan = append(result.Plan, Change{
				Name:    v.name,
				Current: v.currentReplicas,
				Target:  t,
				Delta:   t - v.currentReplicas,
			})
			if t < v.currentReplicas {
				migration.Add(migration, big.NewInt(v.currentReplicas-t))
			}
		}
	}

	// Internal invariant: targets must sum to exactly the requested total.
	if sum.Cmp(big.NewInt(total)) != 0 {
		panic("fedalloc: target sum " + sum.String() + " != requested total " + itoa(total))
	}
	result.TotalMigration = migration.Int64()
	return result
}
