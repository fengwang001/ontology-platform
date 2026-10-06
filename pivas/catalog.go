package pivas

// Drug 是药品目录条目。
type Drug struct {
	RoomStableSec  int
	ColdStableSec  int
	LightSensitive bool
	SolventClass   string
}

type pairKey struct{ a, b string }

func pairKeyOf(x, y string) pairKey {
	if x > y {
		x, y = y, x
	}
	return pairKey{x, y}
}

// drugEntry 记录某药品在某版本起的内容；变更只追加新条目。
type drugEntry struct {
	version int64
	drug    Drug
}

// catalog 管理版本化目录。当前视图直接可变，变更时提升版本号；
// 历史医嘱只保存其受理版本号，按版本二分回查，目录变更绝不追溯。
//
// 禁忌对一旦存在即永久有效（追加新禁忌不会撤销旧禁忌），故只需集合。
type catalog struct {
	version int64
	drugs   map[string][]drugEntry // 按 version 升序
	bad     map[pairKey]int64      // value 为引入版本（保留以备将来支持撤销）
}

func newCatalog() *catalog {
	return &catalog{
		drugs: map[string][]drugEntry{},
		bad:   map[pairKey]int64{},
	}
}

func (c *catalog) bump() int64 {
	c.version++
	return c.version
}

// upsert 登记或更新药品，并返回新版本号。
func (c *catalog) upsert(id string, d Drug) int64 {
	v := c.bump()
	c.drugs[id] = append(c.drugs[id], drugEntry{version: v, drug: d})
	return v
}

func (c *catalog) addBad(a, b string) int64 {
	k := pairKeyOf(a, b)
	if _, ok := c.bad[k]; ok {
		return c.version // 已存在，不产生新约束版本
	}
	v := c.bump()
	c.bad[k] = v
	return v
}

// lookup 取当前版本下的药品。
func (c *catalog) lookup(id string) (Drug, bool) {
	return c.lookupAt(id, c.version)
}

// lookupAt 取指定版本下的药品（版本快照语义）。
func (c *catalog) lookupAt(id string, v int64) (Drug, bool) {
	list := c.drugs[id]
	if len(list) == 0 || list[0].version > v {
		return Drug{}, false
	}
	// 二分：取 version <= v 的最后一个条目。
	lo, hi := 0, len(list)
	for lo < hi {
		mid := (lo + hi) / 2
		if list[mid].version <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return list[lo-1].drug, true
}

// isBadAt 判断某版本下两个药品是否构成禁忌。
func (c *catalog) isBadAt(k pairKey, v int64) bool {
	intro, ok := c.bad[k]
	return ok && intro <= v
}
