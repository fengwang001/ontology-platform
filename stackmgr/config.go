package stackmgr

// Config 描述栈管理子系统的全局配置。
type Config struct {
	BaseSize     int         // 初始栈尺寸（槽位数）
	GrowthFactor int         // 增长倍数 F，F >= 2
	MaxStackSize int         // 单协程上限（槽位）
	TotalQuota   int         // 全局总配额（槽位）
	ShrinkRatio  ShrinkRatio // 收缩触发阈值
}

// ShrinkRatio 用分数表示触发条件：used*Den < size*Num（即 used/size < Num/Den）。
type ShrinkRatio struct {
	Num int
	Den int
}

// validate 读取配置时核验；不合法返回 ClassConfig，实例创建被整体拒绝。
//
// 防抖动条件：增长是把尺寸乘 F，增长后使用率 > 1/F（need > cur/F）。
// 收缩触发条件为 used/size < ratio。要使二者不可能在同一点同时成立，
// 必须严格保证 ratio < 1/F，即 Num*F < Den（整数精确比较，无浮点误差）。
func (c Config) validate() error {
	if c.BaseSize <= 0 {
		return errf(ClassConfig, "config: BaseSize must be positive, got %d", c.BaseSize)
	}
	if c.GrowthFactor < 2 {
		return errf(ClassConfig, "config: GrowthFactor must be >= 2, got %d", c.GrowthFactor)
	}
	if c.TotalQuota <= 0 {
		return errf(ClassConfig, "config: TotalQuota must be positive, got %d", c.TotalQuota)
	}
	if c.MaxStackSize < c.BaseSize {
		return errf(ClassConfig, "config: MaxStackSize %d < BaseSize %d", c.MaxStackSize, c.BaseSize)
	}
	if c.MaxStackSize%c.BaseSize != 0 {
		return errf(ClassConfig, "config: MaxStackSize %d must be a multiple of BaseSize %d",
			c.MaxStackSize, c.BaseSize)
	}
	// 尺寸链 base, base*F, base*F^2, ... 上每个尺寸都必须整除上限，
	// 否则增长到“整除上限的尺寸”后无法保证 next 仍为倍数关系。
	size := c.BaseSize
	for size < c.MaxStackSize {
		if c.MaxStackSize%size != 0 {
			return errf(ClassConfig, "config: stack size %d on growth chain does not divide max %d",
				size, c.MaxStackSize)
		}
		size *= c.GrowthFactor
	}
	if c.ShrinkRatio.Num <= 0 || c.ShrinkRatio.Den <= 0 {
		return errf(ClassConfig, "config: ShrinkRatio terms must be positive, got %d/%d",
			c.ShrinkRatio.Num, c.ShrinkRatio.Den)
	}
	if c.ShrinkRatio.Num >= c.ShrinkRatio.Den {
		return errf(ClassConfig, "config: ShrinkRatio %d/%d must be < 1",
			c.ShrinkRatio.Num, c.ShrinkRatio.Den)
	}
	// 严格不等式：收缩比例必须严格小于增长后使用率下限 1/F。
	if c.ShrinkRatio.Num*c.GrowthFactor >= c.ShrinkRatio.Den {
		return errf(ClassConfig,
			"config: ShrinkRatio %d/%d not strictly below 1/F=%d/%d (thrash possible)",
			c.ShrinkRatio.Num, c.ShrinkRatio.Den, 1, c.GrowthFactor)
	}
	return nil
}
