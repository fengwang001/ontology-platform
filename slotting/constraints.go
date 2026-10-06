package slotting

// validatePallet 校验托盘参数：编号/商品/批次非空，数值为正整数，品类合法。
func validatePallet(p Pallet) error {
	if p.ID == "" || p.Product == "" || p.Batch == "" {
		return errf(ReasonInvalidParam, "pallet id/product/batch must be non-empty")
	}
	if !p.Category.Valid() {
		return errf(ReasonInvalidParam, "invalid pallet category: %d", p.Category)
	}
	if p.Weight <= 0 || p.Height <= 0 {
		return errf(ReasonInvalidParam, "pallet weight/height must be positive")
	}
	return nil
}

// validateConfig 校验货位配置参数。
func validateConfig(c LocationConfig) error {
	if c.Coord.Aisle <= 0 || c.Coord.Level <= 0 || c.Coord.Position <= 0 {
		return errf(ReasonInvalidParam, "coord components must be positive")
	}
	if c.WeightLimit <= 0 || c.ClearHeight <= 0 {
		return errf(ReasonInvalidParam, "weight limit / clear height must be positive")
	}
	if c.Capacity != 1 && c.Capacity != 2 {
		return errf(ReasonInvalidParam, "capacity must be 1 or 2")
	}
	if len(c.Allowed) == 0 {
		return errf(ReasonInvalidParam, "allowed category set must be non-empty")
	}
	for cat := range c.Allowed {
		if !cat.Valid() {
			return errf(ReasonInvalidParam, "invalid allowed category: %d", cat)
		}
	}
	return nil
}

// neighborKeys 返回相邻货位（同通道同层、位序号 ±1）的坐标。
func neighborKeys(c Coord) []Coord {
	return []Coord{
		{Aisle: c.Aisle, Level: c.Level, Position: c.Position - 1},
		{Aisle: c.Aisle, Level: c.Level, Position: c.Position + 1},
	}
}

// conflict 表示放入托盘到某货位时的单项约束判定结果。
type conflict struct {
	frozen   bool
	category bool
	height   bool
	weight   bool
	mix      bool
	adjacent bool
	capacity bool
}

// reason 按拒绝优先级返回首个命中原因；全部通过返回空串。
func (cf conflict) reason() Reason {
	switch {
	case cf.frozen:
		return ReasonFrozen
	case cf.category:
		return ReasonCategoryDenied
	case cf.height:
		return ReasonHeight
	case cf.weight:
		return ReasonWeight
	case cf.mix:
		return ReasonMixConflict
	case cf.adjacent:
		return ReasonAdjacency
	case cf.capacity:
		return ReasonCapacity
	default:
		return ""
	}
}

// checkPut 判定把 p 放入 loc 时的全部单项约束。
// neighbors 为实际存在的相邻货位（无论正常或冻结）。
// 承重与容量的当前占用应已排除"移库托盘自身原位"（由调用方保证）。
func checkPut(loc *locEntry, p Pallet, neighbors []*locEntry) conflict {
	var cf conflict
	cf.frozen = loc.config.Status == StatusFrozen
	cf.category = !loc.config.Allowed[p.Category]
	// 净高对每个托盘独立判定；恰好相等允许。
	cf.height = p.Height > loc.config.ClearHeight
	cf.weight = loc.weight+p.Weight > loc.config.WeightLimit
	cf.capacity = len(loc.pallets) >= loc.config.Capacity
	if len(loc.pallets) > 0 {
		first := loc.pallets[0]
		// 同一货位必须同商品；不允许混批时还须同批次。
		cf.mix = first.Product != p.Product ||
			(!loc.config.AllowMixedBatch && first.Batch != p.Batch)
	}
	// 相邻隔离：易燃与食品不得相邻，对双方放入都检查。
	for _, nb := range neighbors {
		for _, q := range nb.pallets {
			if (p.Category == CategoryFlammable && q.Category == CategoryFood) ||
				(p.Category == CategoryFood && q.Category == CategoryFlammable) {
				cf.adjacent = true
			}
		}
	}
	return cf
}
