// Package rider 实现骑手考核与申诉系统：固定周期扣分、阈值定级、
// 根因簇连带撤销、申诉窗口与已结算周期的回溯补偿。
//
// 典型用法：
//
//	sys, err := rider.New(cfg)
//	sys.RegisterRider(0, "r1")
//	eid, _ := sys.RegisterEvent(3, "r1", 2, rider.EventTypeLateDelivery, "shop-7")
//	aid, _ := sys.SubmitAppeal(10, "r1", eid)
//	comp, _ := sys.RuleAppeal(11, "r1", aid, true)
//	res, _ := sys.Query(20, "r1", 0)
//
// 所有方法可并发调用，语义等价于某种串行交错；错误用 CodeOf 程序化判别。
// 设计取舍与验证方式见同目录 DESIGN.md。
package rider

// EventType 为考核事件类型。
type EventType string

const (
	EventTypeLateDelivery      EventType = "late_delivery"
	EventTypeCustomerComplaint EventType = "customer_complaint"
	EventTypeRejectOrder       EventType = "reject_order"
	EventTypeFaultCancel       EventType = "fault_cancel"
)

// Config 为考核系统的构造参数。
type Config struct {
	PeriodLength         int64             // 周期长度（秒），必须为正
	PeriodOrigin         int64             // 周期起始基准时刻（可为负）
	EventScores          map[EventType]int // 各事件类型扣分值，必须非负且四类齐全
	Thresholds           []int             // 严格递增的定级分数阈值
	AppealWindow         int64             // 申诉窗口时长（秒），必须为正
	ClusterSpan          int64             // 根因簇时间跨度（秒），必须非负
	MaxLevelDrop         int               // 每周期权益等级最多下降的等级数，必须为正
	CompensationPerLevel int               // 每级补偿额，必须非负
}

// Event 表示一条扣分事件。
type Event struct {
	ID       int64
	RiderID  string
	Time     int64
	Type     EventType
	RootKey  string // 根因标识；空表示无根因标识
	Seq      int64  // 登记序号，用于同一时刻的确定性先后
	Revoked  bool
	AppealID int64 // 0 表示尚无申诉
}

// Compensation 为一笔回溯补偿账目。
type Compensation struct {
	AppealID int64
	RiderID  string
	Amount   int
}

// QueryResult 为周期查询结果。
type QueryResult struct {
	Period      int
	Settled     bool
	Score       int
	Level       int
	RightsLevel int
}
