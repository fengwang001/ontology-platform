package federation

import (
	"math/big"
	"sort"
)

// stats 记录一次分配内部的计算轮次与比较规模，用于可验证地证明性能结论：
// 全部计数只与集群数量有关，与 total 的数值大小无关。
type stats struct {
	Clusters     int // 快照中的集群总数（含不可用、已删除）
	Participants int // 参与最小保障的集群数（可用集群）
	Weighted     int // 参与权重分摊的集群数（可用且权重 > 0）
	Rounds       int // 饱和再分配的轮数（上界为 Weighted）
	ComparePairs int // 各轮并列排序的比较对计数之和
	BigIntOps    int // 分摊阶段的大整数乘法/除法次数（线性于各轮参与集群数）
}

// allocate 对一致快照执行纯函数分配，不读取也不修改任何登记状态。
//
// 阶段：
//  1. 最小保障：每个可用集群先得 min；min > 有效上限 => 配置冲突；
//     min 之和 > total => 拒绝（最小副本之和超出总数）。
//  2. 权重分摊：剩余副本在“未饱和且权重 > 0”的集群间按权重比例分摊；
//     份额向下取整，小数部分大者优先补一，并列时当前承载多者优先，
//     仍并列按名称升序；任何集群触及有效上限即饱和，超出部分进入下一轮重分。
//  3. 全部饱和仍有剩余 => 容量不足，错误携带缺口大小。
func allocate(snap []*snapshotCluster, total *big.Int) (map[string]*big.Int, *stats, error) {
	st := &stats{Clusters: len(snap)}
	targets := make(map[string]*big.Int, len(snap))

	// 不可用集群与已删除集群目标恒为零，不参与任何保障与分摊。
	for _, sc := range snap {
		targets[sc.cluster.Name] = big.NewInt(0)
	}

	available := make([]*snapshotCluster, 0, len(snap))
	for _, sc := range snap {
		if !sc.dead && sc.cluster.Available {
			available = append(available, sc)
		}
	}
	st.Participants = len(available)

	// 阶段 1a：配置冲突检查。快照按名称升序，故冲突报告也是确定的。
	caps := make(map[string]*big.Int, len(available))
	for _, sc := range available {
		c := sc.cluster
		capv := c.effectiveCap()
		caps[c.Name] = capv
		if c.Min.Cmp(capv) > 0 {
			return nil, st, configConflict(
				"cluster %q: min %s exceeds effective cap %s",
				c.Name, c.Min, capv)
		}
	}

	// 阶段 1b：发放最小保障并求和。
	sumMin := big.NewInt(0)
	for _, sc := range available {
		c := sc.cluster
		targets[c.Name] = new(big.Int).Set(c.Min)
		sumMin.Add(sumMin, c.Min)
	}
	if sumMin.Cmp(total) > 0 {
		return nil, st, minExceedsTotal(
			"sum of minimum replicas %s exceeds requested total %s", sumMin, total)
	}

	// 权重分摊参与者：可用且权重 > 0；权重为 0 的可用集群只保留最小副本。
	weighted := make([]*Cluster, 0, len(available))
	for _, sc := range available {
		if sc.cluster.Weight.Sign() > 0 {
			weighted = append(weighted, sc.cluster)
		}
	}
	st.Weighted = len(weighted)

	remaining := new(big.Int).Sub(total, sumMin)

	type frac struct {
		c       *Cluster
		num     *big.Int // R*w mod W：小数部分的分子（分母同为 W，直接比分子即可）
		granted *big.Int // 本轮按 floor 得到的份额（+1 在排序后追加）
	}

	// active 始终为尚未饱和（目标 < 有效上限）的权重参与者。
	active := make([]*Cluster, 0, len(weighted))
	for _, c := range weighted {
		if targets[c.Name].Cmp(caps[c.Name]) < 0 {
			active = append(active, c)
		}
	}

	for remaining.Sign() > 0 {
		if len(active) == 0 {
			return nil, st, insufficientCapacity(
				"all eligible clusters saturated; shortfall %s replicas", remaining)
		}
		st.Rounds++
		k := len(active)
		st.ComparePairs += k * (k - 1) / 2

		// 记录本轮开始时每个参与集群的目标，用于最后精确计算实际发放量。
		start := make(map[string]*big.Int, k)
		for _, c := range active {
			start[c.Name] = new(big.Int).Set(targets[c.Name])
		}

		weightSum := big.NewInt(0)
		for _, c := range active {
			weightSum.Add(weightSum, c.Weight)
		}

		rows := make([]*frac, 0, k)
		for _, c := range active {
			num := new(big.Int).Mul(remaining, c.Weight)
			q := new(big.Int).Quo(new(big.Int).Set(num), weightSum)
			num.Mod(num, weightSum)
			st.BigIntOps += 3 // Mul、Quo、Mod 各一次，数值大小不影响调用次数
			rows = append(rows, &frac{c: c, num: num, granted: q})
		}

		// 小数部分从大到小；并列时当前承载多者优先；仍并列按名称升序。
		// 当前承载数只在此处、且仅在小数并列时参与比较。
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].num.Cmp(rows[j].num) != 0 {
				return rows[i].num.Cmp(rows[j].num) > 0
			}
			if rows[i].c.Current.Cmp(rows[j].c.Current) != 0 {
				return rows[i].c.Current.Cmp(rows[j].c.Current) > 0
			}
			return rows[i].c.Name < rows[j].c.Name
		})

		// 第一步：floor 份额（向下取整的确定部分）。floor 超过 headroom 者
		// 直接取有效上限并标记饱和；其 floor 超出部分不在本轮补发。
		next := make([]*Cluster, 0, k)
		saturated := make(map[string]bool, k)
		floorLocked := big.NewInt(0)
		for _, row := range rows {
			c := row.c
			headroom := new(big.Int).Sub(caps[c.Name], start[c.Name])
			if row.granted.Cmp(headroom) >= 0 {
				targets[c.Name] = caps[c.Name]
				saturated[c.Name] = true
				floorLocked.Add(floorLocked, headroom)
			} else {
				targets[c.Name].Add(targets[c.Name], row.granted)
				floorLocked.Add(floorLocked, row.granted)
				next = append(next, c)
			}
		}

		// 第二步：取整余量 L = remaining - 本轮实际锁定的 floor，
		// 严格小于本轮参与集群数，按小数顺序给未饱和集群各补至多 1 个；
		// 已饱和者跳过（它本可能拿到的那个余量进入下一轮）。
		left := new(big.Int).Sub(remaining, floorLocked)

		// 余量补一在小数顺序上持续进行：某集群补一后触顶，剩余余量继续
		// 顺延给后面的未饱和集群；全部候选饱和仍发不完时余量随池进入下一轮。
		for left.Sign() > 0 {
			progressed := false
			for _, row := range rows {
				if left.Sign() == 0 {
					break
				}
				if saturated[row.c.Name] {
					continue
				}
				c := row.c
				if targets[c.Name].Cmp(caps[c.Name]) >= 0 {
					saturated[c.Name] = true
					continue
				}
				targets[c.Name].Add(targets[c.Name], big.NewInt(1))
				left.Sub(left, big.NewInt(1))
				progressed = true
				if targets[c.Name].Cmp(caps[c.Name]) == 0 {
					saturated[c.Name] = true
				}
			}
			if !progressed {
				break
			}
		}

		// 第三步：池扣减量 = 本轮所有参与集群的实际增量之和。
		// floor 截断份额与未发出的余量因此保留在 remaining，下一轮在
		// 仍未饱和的集群间按新权重重算比例。
		distributed := big.NewInt(0)
		for _, row := range rows {
			distributed.Add(distributed,
				new(big.Int).Sub(targets[row.c.Name], start[row.c.Name]))
		}
		remaining.Sub(remaining, distributed)

		// 重建未饱和集合（补一阶段触顶者也退出）。
		stillActive := make([]*Cluster, 0, len(next))
		for _, c := range next {
			if !saturated[c.Name] {
				stillActive = append(stillActive, c)
			}
		}
		active = stillActive
	}

	return targets, st, nil
}
