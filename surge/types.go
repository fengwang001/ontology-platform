// Package surge 实现即时配送的区域运力调度与高峰加价系统。
//
// 系统由四个相互协作的模块组成：
//   - 供需账本（regionState 中的 capacity/pending 计数）
//   - 档位状态机（evaluateTier 的上调即时生效、下调连续确认）
//   - 订单/骑手视角的锁定与结算（orderState.lockedTier、account）
//   - 并发协调（System 级互斥，保证等价串行）
package surge

// RegionID 区域标识。
type RegionID string

// RiderID 骑手标识。
type RiderID string

// OrderID 订单标识。
type OrderID string

// Tier 档位；0 为基础档，1..n 为加价档。
type Tier int

// TimeSec 全局单调的整数秒时刻。
type TimeSec int64

// Config 构造参数。
type Config struct {
	// Thresholds 严格递增的档位触发比率阈值；阈值数量决定加价档数量。
	// 档位 i（i>=1）的触发阈值为 Thresholds[i-1]。
	Thresholds []float64
	// DownConfirmations 档位下降所需的连续确认次数，必须为正。
	DownConfirmations int
	// MaxHeld 骑手同时持单上限，必须为正。
	MaxHeld int
	// Subsidies 每档补贴额，长度必须为 len(Thresholds)+1，均不得为负。
	Subsidies []int64
	// MinEvalInterval 同区域两次评估的最小间隔（含等号），必须非负。
	MinEvalInterval int64
	// Multipliers 可选的每档加价倍率，长度必须为 len(Thresholds)+1；
	// 为 nil 时取基础档 1.0，加价档 i 取其触发阈值 Thresholds[i-1]。
	Multipliers []float64
}

// TierEvent 档位变化事件。
type TierEvent struct {
	Region   RegionID
	At       TimeSec
	FromTier Tier
	ToTier   Tier
}

// SubsidyEntry 一条骑手补贴账目。
type SubsidyEntry struct {
	Order      OrderID
	Region     RegionID
	Tier       Tier
	Amount     int64
	WaivedLate bool // true 表示迟到补贴豁免（补贴为 0）
}

// Snapshot 系统可观测状态，供不变量校验与差分测试使用。
type Snapshot struct {
	Now       TimeSec
	Regions   map[RegionID]RegionSnapshot
	Tiers     map[RegionID]Tier
	Orders    map[OrderID]OrderSnapshot
	Riders    map[RiderID]RiderSnapshot
	HoldCount map[RiderID]int
	Account   map[RiderID]int64
}

// RegionSnapshot 区域供需与档位评估状态。
type RegionSnapshot struct {
	Capacity       int
	Pending        int
	Tier           Tier
	DownConfirmed  int
	LastEvaluateAt TimeSec
}

// OrderSnapshot 订单状态（阶段：P 待派 / D 已派 / C 已完成 / X 已取消）。
type OrderSnapshot struct {
	Region     RegionID
	CreatedAt  TimeSec
	LockedTier Tier
	Stage      byte
	Rider      RiderID
}

// RiderSnapshot 骑手状态（Online=false 表示下线）。
type RiderSnapshot struct {
	Online    bool
	Region    RegionID
	EnteredAt TimeSec
	Held      int
}
