package scheduler

// Config 构造参数，均为整数秒。
type Config struct {
	DispatchLead int64 // 派单提前量：时段起点前多久释放派单
	CutoffLead   int64 // 改期截止提前量：时段起点前多久停止改期
	EarliestLead int64 // 最早可预约提前量
	LatestLead   int64 // 最晚可预约提前量
	MaxShiftSpan int64 // 顺延候选最大跨度
}

func (c Config) validate() error {
	if c.DispatchLead < 0 || c.CutoffLead < 0 || c.EarliestLead < 0 || c.MaxShiftSpan < 0 {
		return newError(CodeInvalidParam, "leads and shift span must be non-negative")
	}
	if c.LatestLead < c.EarliestLead {
		return newError(CodeInvalidParam, "latest lead must be >= earliest lead")
	}
	return nil
}

// SlotView 时段名额只读视图。
type SlotView struct {
	Start    int64
	End      int64
	Cap      int
	Occupied int
	Oversold bool // 已占 > 上限（平台调低上限后可能出现）
}

// ReservationView 预约只读视图。
type ReservationView struct {
	ID                   string
	RegionID             string
	SlotStart            int64 // 当前时段起点
	Shifted              bool  // 下单时是否发生了顺延
	OrigSlotStart        int64 // 下单时请求的原始时段起点
	Released             bool  // 按查询时刻判定是否已释放派单
	Canceled             bool
	CanceledAfterRelease bool // 是否为释放后取消
	Delivered            bool
}

// PlaceResult 下单结果。
type PlaceResult struct {
	RegionID      string
	SlotStart     int64 // 实际落位时段起点
	Shifted       bool  // 是否顺延落位
	OrigSlotStart int64 // 用户请求的原始时段起点
}
