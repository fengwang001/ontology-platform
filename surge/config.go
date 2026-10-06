package surge

// Config 为系统构造参数。
//
// Thresholds 为严格递增的档位触发比率阈值（不含基础档）：
// 阈值数为 n 时共有 n+1 个档位，档位 0 为基础档。
// DowngradeConfirms 为目标档低于当前档时所需的连续确认次数。
// MaxHeldOrders 为单个骑手同时持单上限。
// Subsidies[k] 为锁定档 k 对应的每单补贴额，长度必须等于档位数 len(Thresholds)+1。
// MinEvalInterval 为同一区域两次评估之间的最小间隔（秒，取等合法）。
type Config struct {
	Thresholds        []float64
	DowngradeConfirms int
	MaxHeldOrders     int
	Subsidies         []int64
	MinEvalInterval   int64
}

func (c Config) validate() *Error {
	if c.DowngradeConfirms <= 0 {
		return errf(KindInvalidParam, "downgrade confirms must be positive")
	}
	if c.MaxHeldOrders <= 0 {
		return errf(KindInvalidParam, "max held orders must be positive")
	}
	if c.MinEvalInterval <= 0 {
		return errf(KindInvalidParam, "min eval interval must be positive")
	}
	if len(c.Thresholds) == 0 {
		return errf(KindInvalidParam, "thresholds must not be empty")
	}
	for i, t := range c.Thresholds {
		if i > 0 && t <= c.Thresholds[i-1] {
			return errf(KindInvalidParam, "thresholds must be strictly increasing")
		}
	}
	if len(c.Subsidies) != len(c.Thresholds)+1 {
		return errf(KindInvalidParam, "subsidies length must equal tiers count")
	}
	for _, s := range c.Subsidies {
		if s < 0 {
			return errf(KindInvalidParam, "subsidy must be non-negative")
		}
	}
	return nil
}

func (c Config) tiers() int { return len(c.Thresholds) + 1 }
