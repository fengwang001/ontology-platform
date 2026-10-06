package medsched

// Drug 描述药品目录中的一种药品。
type Drug struct {
	Category    string // 类别标识
	MinInterval int64  // 最小安全给药间隔（秒，正整数）
}

// Catalog 是药品目录：药品 -> 类别与最小安全间隔。
type Catalog struct {
	drugs map[string]Drug
}

// NewCatalog 创建空药品目录。
func NewCatalog() *Catalog {
	return &Catalog{drugs: make(map[string]Drug)}
}

// RegisterDrug 登记一种药品。重复登记视为状态不符。
func (c *Catalog) RegisterDrug(drugID, category string, minInterval int64) error {
	if drugID == "" || category == "" || minInterval <= 0 {
		return errf(ErrInvalidParam, "药品登记参数非法")
	}
	if _, ok := c.drugs[drugID]; ok {
		return errf(ErrInvalidState, "药品已登记: %s", drugID)
	}
	c.drugs[drugID] = Drug{Category: category, MinInterval: minInterval}
	return nil
}

// Get 查询药品；不存在返回 ErrNotFound。
func (c *Catalog) Get(drugID string) (Drug, error) {
	d, ok := c.drugs[drugID]
	if !ok {
		return Drug{}, errf(ErrNotFound, "药品不存在: %s", drugID)
	}
	return d, nil
}
