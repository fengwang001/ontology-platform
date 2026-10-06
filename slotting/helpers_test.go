package slotting

// 测试辅助构造器。

func cats(cs ...Category) map[Category]bool {
	m := map[Category]bool{}
	for _, c := range cs {
		m[c] = true
	}
	return m
}

func locCfg(aisle, level, pos, wlimit, height int, cap2 bool,
	allowed map[Category]bool, mixedBatch bool) LocationConfig {
	capacity := 1
	if cap2 {
		capacity = 2
	}
	return LocationConfig{
		Coord:           Coord{Aisle: aisle, Level: level, Position: pos},
		WeightLimit:     wlimit,
		ClearHeight:     height,
		Allowed:         allowed,
		Capacity:        capacity,
		AllowMixedBatch: mixedBatch,
		Status:          StatusNormal,
	}
}

func newSvcWith(cfgs ...LocationConfig) *Service {
	s := NewService(NewStore())
	for _, c := range cfgs {
		if err := s.Store().AddLocation(c); err != nil {
			panic(err)
		}
	}
	return s
}

func pallet(id, product, batch string, cat Category, w, h int) Pallet {
	return Pallet{ID: id, Product: product, Batch: batch, Category: cat, Weight: w, Height: h}
}
