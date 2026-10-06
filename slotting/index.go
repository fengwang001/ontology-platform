package slotting

import "math"

// emptyIndex 维护“状态正常且当前为空”的货位，按字典序做 sqrt 分块。
//
// 每个块对每个允许品类保存一条 skyline：
// 先按净高 h 降序扫描，仅当承重 w 严格大于已有最大值时保留，反转后得到
// h 升序、w 降序的点序列。查询 (H, W) 时，二分定位第一个 h>=H 的点，
// 该点即后缀（h>=H 的所有点）中 w 最大者，一次比较即可判定。
//
// firstFeasible 顺序扫块（每块 O(1) 判定），命中后仅在该块内（≤2B 个货位）
// 逐个确认，因此考察货位数为 O(sqrt(N))，与在位托盘总数及无关商品无关。
type emptyIndex struct {
	locs    map[LocationID]*locState
	pallets map[string]*Pallet
	blocks  []*emptyBlock
	b       int // 目标块长，约为 sqrt(货位总数)
}

type point struct{ h, w int }

type emptyBlock struct {
	ids       []LocationID // 块内按字典序有序
	sky       map[Category][]point
	maxWeight map[Category]int
}

func newEmptyIndex(locs map[LocationID]*locState, pallets map[string]*Pallet) *emptyIndex {
	idx := &emptyIndex{locs: locs, pallets: pallets}
	idx.rebuild()
	return idx
}

func (idx *emptyIndex) targetB() int {
	n := len(idx.locs)
	if n < 64 {
		return 4 // 小规模下保持块小，测试稳定
	}
	b := int(math.Ceil(math.Sqrt(float64(n))))
	if b < 8 {
		b = 8
	}
	return b
}

// rebuild 全量重建，仅在新增货位导致规模档变化时使用，正常上下架不触发。
func (idx *emptyIndex) rebuild() {
	idx.b = idx.targetB()
	all := make([]LocationID, 0)
	for id, ls := range idx.locs {
		if ls.loc.Status == StatusNormal && len(ls.pallets) == 0 {
			all = append(all, id)
		}
	}
	sortIDs(all)
	idx.blocks = idx.blocks[:0]
	for start := 0; start < len(all); start += idx.b {
		end := start + idx.b
		if end > len(all) {
			end = len(all)
		}
		idx.blocks = append(idx.blocks, idx.makeBlock(all[start:end]))
	}
}

func (idx *emptyIndex) makeBlock(ids []LocationID) *emptyBlock {
	cp := make([]LocationID, len(ids))
	copy(cp, ids)
	bl := &emptyBlock{ids: cp, sky: map[Category][]point{}, maxWeight: map[Category]int{}}
	byCat := map[Category][]point{}
	for _, id := range cp {
		ls := idx.locs[id]
		for _, c := range ls.loc.Allowed {
			if !idx.adjacencySafe(id, c) {
				continue
			}
			byCat[c] = append(byCat[c], point{h: ls.loc.ClearHeight, w: ls.loc.MaxWeight})
			if ls.loc.MaxWeight > bl.maxWeight[c] {
				bl.maxWeight[c] = ls.loc.MaxWeight
			}
		}
	}
	for c, pts := range byCat {
		// h 降序，h 相同保留 w 较大者在前。
		for i := 1; i < len(pts); i++ {
			for j := i; j > 0 && (pts[j].h > pts[j-1].h ||
				(pts[j].h == pts[j-1].h && pts[j].w > pts[j-1].w)); j-- {
				pts[j], pts[j-1] = pts[j-1], pts[j]
			}
		}
		var sky []point
		best := 0
		for _, pt := range pts {
			if pt.w > best {
				sky = append(sky, pt)
				best = pt.w
			}
		}
		// 反转为 h 升序、w 降序。
		for i, j := 0, len(sky)-1; i < j; i, j = i+1, j-1 {
			sky[i], sky[j] = sky[j], sky[i]
		}
		bl.sky[c] = sky
	}
	return bl
}

// adjacencySafe 判断空货位 id 若放入品类 c 的托盘，是否与在位邻居冲突。
func (idx *emptyIndex) adjacencySafe(id LocationID, c Category) bool {
	conflictWith := Category("")
	switch c {
	case CatFood:
		conflictWith = CatFlammable
	case CatFlammable:
		conflictWith = CatFood
	default:
		return true
	}
	for _, d := range []int{-1, 1} {
		nid := LocationID{id.Aisle, id.Level, id.Index + d}
		ns := idx.locs[nid]
		if ns == nil {
			continue
		}
		for _, pid := range ns.pallets {
			if idx.pallets[pid].Category == conflictWith {
				return false
			}
		}
	}
	return true
}

// can 判定块内是否存在品类允许且净高>=h、承重>=w 的货位。
func (bl *emptyBlock) can(c Category, h, w int) bool {
	sky := bl.sky[c]
	if len(sky) == 0 {
		return false
	}
	// h 升序；二分第一个 h>=h。
	lo, hi := 0, len(sky)
	for lo < hi {
		mid := (lo + hi) / 2
		if sky[mid].h < h {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(sky) {
		return false
	}
	return sky[lo].w >= w // w 降序，首点即后缀最大承重
}

// locate 返回 id 应处/所在的块下标，及块内插入位置。
func (idx *emptyIndex) locate(id LocationID) (blockPos, insertPos int) {
	lo, hi := 0, len(idx.blocks)
	for lo < hi {
		mid := (lo + hi) / 2
		bl := idx.blocks[mid]
		if len(bl.ids) > 0 && id.Less(bl.ids[0]) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	blockPos = lo - 1
	if blockPos < 0 {
		return 0, 0
	}
	bl := idx.blocks[blockPos]
	k := 0
	for k < len(bl.ids) && bl.ids[k].Less(id) {
		k++
	}
	if k == len(bl.ids) && blockPos+1 < len(idx.blocks) {
		blockPos++
		k = 0
	}
	return blockPos, k
}

func (idx *emptyIndex) add(id LocationID) {
	if idx.b == 0 {
		idx.rebuild()
		return
	}
	if nb := idx.targetB(); nb != idx.b {
		idx.rebuild()
		if idx.contains(id) {
			return
		}
	}
	var bp, k int
	if len(idx.blocks) == 0 {
		idx.blocks = append(idx.blocks, &emptyBlock{ids: []LocationID{}, sky: map[Category][]point{}, maxWeight: map[Category]int{}})
	} else {
		bp, k = idx.locate(id)
	}
	bl := idx.blocks[bp]
	if k < len(bl.ids) && bl.ids[k] == id {
		return
	}
	bl.ids = append(bl.ids, LocationID{})
	copy(bl.ids[k+1:], bl.ids[k:])
	bl.ids[k] = id
	if len(bl.ids) > 2*idx.b {
		mid := len(bl.ids) / 2
		left := idx.makeBlock(bl.ids[:mid])
		right := idx.makeBlock(bl.ids[mid:])
		idx.blocks = append(idx.blocks, nil)
		copy(idx.blocks[bp+2:], idx.blocks[bp+1:])
		idx.blocks[bp], idx.blocks[bp+1] = left, right
	} else {
		idx.blocks[bp] = idx.makeBlock(bl.ids)
	}
}

func (idx *emptyIndex) remove(id LocationID) {
	bp, k := idx.locate(id)
	if bp < 0 || bp >= len(idx.blocks) {
		return
	}
	bl := idx.blocks[bp]
	if k >= len(bl.ids) || bl.ids[k] != id {
		return
	}
	bl.ids = append(bl.ids[:k], bl.ids[k+1:]...)
	switch {
	case len(bl.ids) == 0:
		idx.blocks = append(idx.blocks[:bp], idx.blocks[bp+1:]...)
	case bp+1 < len(idx.blocks) && len(bl.ids)+len(idx.blocks[bp+1].ids) <= 2*idx.b:
		merged := append(append([]LocationID{}, bl.ids...), idx.blocks[bp+1].ids...)
		idx.blocks[bp] = idx.makeBlock(merged)
		idx.blocks = append(idx.blocks[:bp+1], idx.blocks[bp+2:]...)
	default:
		idx.blocks[bp] = idx.makeBlock(bl.ids)
	}
}

func (idx *emptyIndex) contains(id LocationID) bool {
	bp, k := idx.locate(id)
	return bp >= 0 && bp < len(idx.blocks) && k < len(idx.blocks[bp].ids) && idx.blocks[bp].ids[k] == id
}

// changed 在货位 id 的在位情况变化后，重建其自身与两个邻居所在块的 skyline。
// 一次放入/取出至多影响这些块，代价 O(B)，与货位总数无关。
func (idx *emptyIndex) changed(id LocationID) {
	targets := []LocationID{id,
		{id.Aisle, id.Level, id.Index - 1},
		{id.Aisle, id.Level, id.Index + 1},
	}
	done := map[int]bool{}
	for _, tid := range targets {
		if _, ok := idx.locs[tid]; !ok {
			continue
		}
		bp, _ := idx.locate(tid)
		if bp < 0 || bp >= len(idx.blocks) || done[bp] {
			continue
		}
		done[bp] = true
		old := idx.blocks[bp]
		idx.blocks[bp] = idx.makeBlock(old.ids)
	}
}

// firstFeasible 返回字典序最小的可行空货位；
// 块 skyline 已同时覆盖静态条件与当前相邻隔离，故命中块内逐位确认必然通过。
// examined 为实际逐位确认的货位数，量级 O(sqrt(N))。
func (idx *emptyIndex) firstFeasible(c Category, h, w int) (LocationID, int) {
	examined := 0
	for _, bl := range idx.blocks {
		if !bl.can(c, h, w) {
			continue
		}
		for _, id := range bl.ids {
			ls := idx.locs[id]
			examined++
			if ls.loc.Status != StatusNormal || len(ls.pallets) != 0 {
				continue
			}
			if categoryAllowed(ls.loc.Allowed, c) &&
				ls.loc.ClearHeight >= h && ls.loc.MaxWeight >= w &&
				idx.adjacencySafe(id, c) {
				return id, examined
			}
		}
	}
	return LocationID{}, examined
}
