package locker

// Config 为柜体的固定参数，均为非负整数秒/货币单位。
type Config struct {
	// FreeStorage 免费保管时长（秒），恰好满免费时长不收费。
	FreeStorage int64
	// BillingPeriod 滞留费计费周期（秒，>0）。
	BillingPeriod int64
	// FeePerPeriod 每个完整计费周期的单价。
	FeePerPeriod int64
	// FeeCap 单件滞留费封顶；<=0 表示不封顶。
	FeeCap int64
	// MaxStorage 保管总时长上限（秒，>0），恰好满即超时。
	MaxStorage int64
	// CodeCooldown 取件码失效后的冷却时长（秒），恰好满冷却即可再用。
	CodeCooldown int64
	// CodeCount 取件码总数（>0）。
	CodeCount int
}

func (c Config) validate() bool {
	return c.BillingPeriod > 0 && c.MaxStorage > 0 && c.CodeCount > 0 &&
		c.FreeStorage >= 0 && c.FeePerPeriod >= 0 && c.CodeCooldown >= 0
}

// DefaultConfig 给出一组示例参数。
func DefaultConfig() Config {
	return Config{
		FreeStorage:   12 * 3600,
		BillingPeriod: 12 * 3600,
		FeePerPeriod:  1,
		FeeCap:        10,
		MaxStorage:    7 * 24 * 3600,
		CodeCooldown:  3600,
		CodeCount:     10000,
	}
}
