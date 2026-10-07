package lab

// ItemSpec 是检验项目在目录中的登记信息。
type ItemSpec struct {
	TubeType     string // 所需管类别
	MaxDelivery  int64  // 采集后最大送达秒数（正整数）
	RequireCold  bool   // 是否必须冷藏运送
	MaxHemolysis int    // 可容忍的溶血等级（0..4）
}

// Catalog 是检验项目目录。目录变更只对变更之后提交的申请生效，
// 已提交申请持有提交时刻的快照，不追溯。
type Catalog struct {
	specs map[string]ItemSpec
}

func newCatalog() *Catalog {
	return &Catalog{specs: make(map[string]ItemSpec)}
}

// upsert 登记或变更一个项目。
func (c *Catalog) upsert(itemID string, spec ItemSpec) {
	c.specs[itemID] = spec
}

// snapshot 取项目当前登记信息的快照。
func (c *Catalog) snapshot(itemID string) (ItemSpec, bool) {
	spec, ok := c.specs[itemID]
	return spec, ok
}
