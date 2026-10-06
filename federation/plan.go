package federation

import (
	"math/big"
	"sort"
)

// Change 描述单个集群相对当前承载副本数的增减；目标与当前相同的集群不出现。
type Change struct {
	Name  string
	From  *big.Int
	To    *big.Int
	Delta *big.Int // To - From；迁出集群（含不可用/已删除）为负数
}

// Result 是一次分配的完整结果。Targets 覆盖快照内所有集群（含目标为零者），
// Changes 只包含目标与当前不同的集群，按名称升序，保证结果不受输入顺序影响。
type Result struct {
	Targets   map[string]*big.Int
	Changes   []*Change
	Migration *big.Int // 总迁移量：所有减少量（From-To）之和
}

func buildPlan(snap []*snapshotCluster, targets map[string]*big.Int) *Result {
	out := &Result{
		Targets:   make(map[string]*big.Int, len(targets)),
		Changes:   []*Change{},
		Migration: big.NewInt(0),
	}
	for name, v := range targets {
		out.Targets[name] = new(big.Int).Set(v)
	}

	for _, sc := range snap {
		c := sc.cluster
		to := targets[c.Name]
		if to == nil {
			to = big.NewInt(0)
		}
		if c.Current.Cmp(to) == 0 {
			continue
		}
		ch := &Change{
			Name:  c.Name,
			From:  new(big.Int).Set(c.Current),
			To:    new(big.Int).Set(to),
			Delta: new(big.Int).Sub(to, c.Current),
		}
		out.Changes = append(out.Changes, ch)
		if ch.Delta.Sign() < 0 {
			out.Migration.Add(out.Migration, new(big.Int).Neg(ch.Delta))
		}
	}
	sort.Slice(out.Changes, func(i, j int) bool {
		return out.Changes[i].Name < out.Changes[j].Name
	})
	return out
}
