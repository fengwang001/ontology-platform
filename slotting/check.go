package slotting

func categoryAllowed(allowed []Category, c Category) bool {
	for _, a := range allowed {
		if a == c {
			return true
		}
	}
	return false
}

// conflicts 返回两个品类是否构成易燃-食品相邻隔离冲突。
func conflicts(a, b Category) bool {
	return (a == CatFlammable && b == CatFood) || (a == CatFood && b == CatFlammable)
}

// checkPlace 按“放入指定货位”的优先级检查全部约束（不含货位是否存在）。
// rejectFrozen=true 时（自动/指定上架）冻结货位被拒绝；
// 移库的目标检查同样禁止冻结（只有取出/移出允许），故恒为 true，
// 该参数保留语义说明。邻接检查基于 st 当前快照，移库时调用方先构造“已离开原位”的快照。
func checkPlace(st *store, ls *locState, p *Pallet, rejectFrozen bool) error {
	if rejectFrozen && ls.loc.Status == StatusFrozen {
		return fail(ReasonFrozen, "货位 %s 已冻结", ls.loc.ID)
	}
	if !categoryAllowed(ls.loc.Allowed, p.Category) {
		return fail(ReasonCategoryNotAllowed, "货位 %s 不允许品类 %s", ls.loc.ID, p.Category)
	}
	if p.Height > ls.loc.ClearHeight {
		return fail(ReasonHeightInsufficient,
			"托盘高 %d > 货位 %s 净高 %d", p.Height, ls.loc.ID, ls.loc.ClearHeight)
	}
	if ls.weight+p.Weight > ls.loc.MaxWeight {
		return fail(ReasonWeightInsufficient,
			"货位 %s 剩余承重 %d < 托盘重量 %d", ls.loc.ID, ls.loc.MaxWeight-ls.weight, p.Weight)
	}
	if len(ls.pallets) > 0 {
		first := st.pallets[ls.pallets[0]]
		if first.Product != p.Product {
			return fail(ReasonMixConflict, "货位 %s 已存商品 %s，不能放入商品 %s",
				ls.loc.ID, first.Product, p.Product)
		}
		if !ls.loc.AllowMix {
			for _, pid := range ls.pallets {
				if ex := st.pallets[pid]; ex.Batch != p.Batch {
					return fail(ReasonMixConflict, "货位 %s 不允许混批：%s 与 %s",
						ls.loc.ID, ex.Batch, p.Batch)
				}
			}
		}
	}
	// 相邻隔离：位序号 ±1 的至多两个货位点查即可，不随货位总数增长。
	for _, delta := range []int{-1, 1} {
		nid := LocationID{ls.loc.ID.Aisle, ls.loc.ID.Level, ls.loc.ID.Index + delta}
		ns := st.locations[nid]
		if ns == nil || len(ns.pallets) == 0 {
			continue
		}
		for _, npid := range ns.pallets {
			if conflicts(p.Category, st.pallets[npid].Category) {
				return fail(ReasonAdjacencyConflict,
					"货位 %s 的托盘（%s）与相邻货位 %s 的托盘（%s）冲突",
					ls.loc.ID, p.Category, nid, st.pallets[npid].Category)
			}
		}
	}
	if len(ls.pallets) >= ls.loc.Capacity {
		return fail(ReasonCapacityFull, "货位 %s 容量 %d 已满", ls.loc.ID, ls.loc.Capacity)
	}
	return nil
}
