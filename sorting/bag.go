package sorting

// bag 集袋内部表示。空袋不会存在：创建即放入首件。
type bag struct {
	id           int64
	site         string
	status       BagStatus
	waybills     []string        // 按加入顺序
	inBag        map[string]bool // 袋内运单号集合，O(1) 判定
	totalWeight  int64
	hasFragile   bool
	hasLiquid    bool
	firstAddTime int64
	sealTime     int64
	departTime   int64
	trainNo      string
	unpackTime   int64
	unpackSite   string
	missing      []string
	extra        []string
}

func newBag(id int64, site string, now int64) *bag {
	return &bag{
		id:           id,
		site:         site,
		status:       BagOpen,
		inBag:        make(map[string]bool),
		firstAddTime: now,
		sealTime:     -1,
		departTime:   -1,
		unpackTime:   -1,
	}
}

// canAccept 报告在不做任何状态变更的前提下，p 能否直接放入本袋。
// 触发换袋的条件：件数超限、总重超限、品类互斥、停留时限已满（恰好满即到时）。
func (b *bag) canAccept(p Parcel, cfg Config, now int64) bool {
	if len(b.waybills)+1 > cfg.MaxBagCount {
		return false
	}
	if b.totalWeight+p.Weight > cfg.MaxBagWeight {
		return false
	}
	if p.Category == CategoryFragile && b.hasLiquid {
		return false
	}
	if p.Category == CategoryLiquid && b.hasFragile {
		return false
	}
	if now-b.firstAddTime >= cfg.DwellLimit {
		return false
	}
	return true
}

func (b *bag) add(p Parcel) {
	b.waybills = append(b.waybills, p.Waybill)
	b.inBag[p.Waybill] = true
	b.totalWeight += p.Weight
	switch p.Category {
	case CategoryFragile:
		b.hasFragile = true
	case CategoryLiquid:
		b.hasLiquid = true
	}
}

// snapshot 生成只读副本，调用方持有期间不随场内状态变化。
func (b *bag) snapshot() BagInfo {
	info := BagInfo{
		ID:           b.id,
		Site:         b.site,
		Status:       b.status,
		TotalWeight:  b.totalWeight,
		FirstAddTime: b.firstAddTime,
		SealTime:     b.sealTime,
		DepartTime:   b.departTime,
		TrainNo:      b.trainNo,
		UnpackTime:   b.unpackTime,
		UnpackSite:   b.unpackSite,
	}
	info.Waybills = append([]string(nil), b.waybills...)
	info.UnpackMissing = append([]string(nil), b.missing...)
	info.UnpackExtra = append([]string(nil), b.extra...)
	return info
}
