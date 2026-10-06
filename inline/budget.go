package inline

import "errors"

// Config 是一次决策任务的固定预算规则。
type Config struct {
	// CallOverhead 是每次内联从调用者尺寸中减去的固定调用开销。
	CallOverhead int64
	// 增长预算 = 初始尺寸 * GrowthNum / GrowthDen，
	// 判定式为 growth*GrowthDen <= GrowthNum*initialSize（取等允许）。
	GrowthNum int64
	GrowthDen int64
	// MaxSize 是任一函数最终尺寸的全局绝对上限（取等允许）。
	MaxSize int64
	// MaxChainRepeat 是同一函数在同一展开链上允许出现的最大次数。
	MaxChainRepeat int
}

func (c Config) validate() error {
	switch {
	case c.CallOverhead < 0:
		return errors.New("inline: CallOverhead must be >= 0")
	case c.GrowthNum < 0:
		return errors.New("inline: GrowthNum must be >= 0")
	case c.GrowthDen <= 0:
		return errors.New("inline: GrowthDen must be > 0")
	case c.MaxSize < 0:
		return errors.New("inline: MaxSize must be >= 0")
	case c.MaxChainRepeat < 1:
		return errors.New("inline: MaxChainRepeat must be >= 1")
	}
	return nil
}

// accountant 核算单个根函数的尺寸与增长。
// 每次预算判定都是 O(1) 的整数运算，与全程序函数总数无关。
type accountant struct {
	cfg     Config
	initial int64
	size    int64
	growth  int64
}

func newAccountant(cfg Config, initial int64) *accountant {
	return &accountant{cfg: cfg, initial: initial, size: initial}
}

// withinBudget 判定在调用者当前尺寸基础上再增长 delta 是否仍在
// 两条硬约束之内：增长总量不超过初始尺寸的固定倍数，且绝对尺寸
// 不超过全局上限。取等允许。
func (a *accountant) withinBudget(delta int64) bool {
	return (a.growth+delta)*a.cfg.GrowthDen <= a.cfg.GrowthNum*a.initial &&
		a.size+delta <= a.cfg.MaxSize
}

// apply 把一次内联引起的尺寸变化计入调用者。
func (a *accountant) apply(delta int64) {
	a.size += delta
	a.growth += delta
}
