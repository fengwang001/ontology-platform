package matching

// Side 委托方向。
type Side int

const (
	Buy Side = iota + 1
	Sell
)

// OrderType 委托类型。
type OrderType int

const (
	// Limit 普通限价委托：全部量都是显示量。
	Limit OrderType = iota + 1
	// Iceberg 冰山委托：以显示批为单位排队，批次成交后补批并失去时间优先。
	Iceberg
	// Hidden 隐藏委托：不产生任何公开显示量，仅在价位显示队列耗尽后成交。
	Hidden
)

// Status 委托生命周期状态。
type Status int

const (
	StatusPending   Status = iota + 1 // 挂簿（尚未成交）
	StatusPartial                     // 部分成交
	StatusCompleted                   // 已完成
	StatusCancelled                   // 已撤销
)

// OrderParams 新委托入参。
type OrderParams struct {
	// ClientID 调用方给出的唯一编号，必须为正。
	ClientID int64
	// Side 买卖方向。
	Side Side
	// Price 正整数限价。
	Price int64
	// TotalQty 正整数委托总量。
	TotalQty int64
	// Type 委托类型。
	Type OrderType
	// IcebergVisibleQty 冰山委托的显示量；仅 Type==Iceberg 时使用，
	// 必须满足 1 <= IcebergVisibleQty <= TotalQty。
	IcebergVisibleQty int64
}

// Order 是一个被接受委托的可查询快照。
type Order struct {
	ClientID     int64
	Seq          int64
	Side         Side
	Price        int64
	Type         OrderType
	TotalQty     int64 // 当前总量（改量后可能变化）
	FilledQty    int64 // 已成交量
	RemainingQty int64 // 剩余总量
	VisibleQty   int64 // 冰山原始显示量参数
	Status       Status
}

// Fill 一笔成交（一次显示批或一个隐藏委托剩余量的撮合结果）。
type Fill struct {
	Seq          int64 // 全局成交序号，从 1 开始
	BuyClientID  int64
	SellClientID int64
	Price        int64 // 成交价，取被动方价格
	Qty          int64
	Aggressor    Side  // 主动委托方向
	AggClientID  int64 // 主动方客户端编号
}

// DepthAtPrice 某价位的公开盘口信息。
type DepthAtPrice struct {
	Price      int64
	VisibleQty int64 // 仅当前显示批，不含冰山储备与隐藏量
}
