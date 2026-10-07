package inventory

// plan 为一张订单计算发货分配，不落账。
//
// 订单行按给定次序依次处理，后面的行看到前面行已占用后的可承诺量
// （通过 remaining 缓存按（仓库，商品）惰性求值并递减实现）。
//
// 返回值：分配方案；failLine 为无法满足的订单行下标（-1 表示全部满足）；
// permanent 标记该缺货是永久缺货（true）还是暂时缺货（false）。
// 本方法只读系统状态（除惰性清理失效预留这一语义中性的内部优化外）。
func (s *System) plan(lines []OrderLine, commitAt int64, allowSplit bool) (allocs []Allocation, failLine int, permanent bool) {
	type bucketKey struct {
		warehouse string
		product   string
	}
	remaining := make(map[bucketKey]int64)
	avail := func(w *Warehouse, product string) int64 {
		k := bucketKey{w.id, product}
		v, ok := remaining[k]
		if !ok {
			v = w.bucket(product).atp(commitAt, s.clock, &s.m)
			remaining[k] = v
		}
		return v
	}
	take := func(w *Warehouse, product string, qty int64) {
		remaining[bucketKey{w.id, product}] -= qty
	}

	// consumed 记录本订单已占用各商品的数量，用于缺货分类时
	// 从总供给中扣除（同一订单内后面的行看到前面行已占用后的量）。
	consumed := make(map[string]int64)

	for i, line := range lines {
		if !allowSplit {
			// 不允许拆分：单一仓库全量满足，取可承诺量足够者中优先序号最小者。
			var chosen *Warehouse
			for _, w := range s.sorted {
				if avail(w, line.Product) >= line.Qty {
					chosen = w
					break
				}
			}
			if chosen == nil {
				return nil, i, s.isPermanentShortage(line.Product, line.Qty, consumed[line.Product])
			}
			take(chosen, line.Product, line.Qty)
			allocs = append(allocs, Allocation{LineIndex: i, Warehouse: chosen.id, Product: line.Product, Qty: line.Qty})
		} else {
			// 允许拆分：按仓库优先序号由小到大依次尽量取用，直到满足。
			need := line.Qty
			for _, w := range s.sorted {
				if need == 0 {
					break
				}
				a := avail(w, line.Product)
				if a <= 0 {
					continue
				}
				q := a
				if q > need {
					q = need
				}
				take(w, line.Product, q)
				allocs = append(allocs, Allocation{LineIndex: i, Warehouse: w.id, Product: line.Product, Qty: q})
				need -= q
			}
			if need > 0 {
				return nil, i, s.isPermanentShortage(line.Product, line.Qty, consumed[line.Product])
			}
		}
		consumed[line.Product] += line.Qty
	}
	return allocs, -1, false
}

// isPermanentShortage 判定缺货类别：
// 所有仓库现货加全部计划入库的总和（忽略预留与到货时刻），
// 扣除本订单前面行已占用的同商品数量后，仍不足 qty 则为永久缺货，
// 否则为暂时缺货（受预留或到货时刻限制）。
func (s *System) isPermanentShortage(product string, qty, alreadyConsumed int64) bool {
	var total int64
	for _, w := range s.sorted {
		if b := w.findBucket(product); b != nil {
			total += b.totalSupply()
		}
	}
	return total-alreadyConsumed < qty
}
