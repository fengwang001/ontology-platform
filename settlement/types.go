package settlement

// Config 商户级配置。
type Config struct {
	// DelayDays 结算延迟 N（营业日数），不小于 1。
	DelayDays int
	// ReserveBps 保证金留存比例（基点，万分之一），取值 [0, 10000]。
	ReserveBps int
	// HorizonDays 保证金滚动期 H（营业日数），不小于 1。
	HorizonDays int
}

func (c Config) valid() bool {
	return c.DelayDays >= 1 && c.HorizonDays >= 1 && c.ReserveBps >= 0 && c.ReserveBps <= 10000
}

// PayoutRecord 每个营业日结算产生的一条出款记录。
type PayoutRecord struct {
	MerchantID string
	Day        int64 // 结算营业日 t
	Net        int64 // 可结算净额（上一日结转负余额 + 新到期流水之和）
	Release    int64 // 本日到期释放额
	NewReserve int64 // 本日新留存额（仅 Net >= 0 时可能非零）
	Drawn      int64 // 本日动用保证金总额（仅 Net < 0 时可能非零）
	Payout     int64 // 出款额（恒非负）
	CarryAfter int64 // 结算后结转到下一营业日的负余额（<= 0）
}

// ReserveBatch 一个保证金批次的快照。
type ReserveBatch struct {
	RetentionDay int64 // 留存日
	ReleaseDay   int64 // 到期释放日（留存日之后第 H 个营业日）；不存在时为 math.MaxInt64
	Balance      int64 // 未动用余额
}

// Snapshot 商户某一时刻的可复现状态快照。
type Snapshot struct {
	MerchantID       string
	LastSettledDay   int64 // 已结算处理到的最近营业日；未结算过为 math.MinInt64
	CumulativePayout int64 // 累计出款
	ReserveBalance   int64 // 保证金余额（全部批次未动用余额之和）
	Carry            int64 // 结转负余额（<= 0）
	SettledTxSum     int64 // 已结算流水净额（用于不变式校验）
	PendingTxCount   int   // 尚未结算的流水笔数
	Batches          []ReserveBatch
}
