// Package baggage 实现联程行李中转与超重计费。
//
// 核心概念：一条多航段行程在办理托运时被拆成若干“托运段”（section），
// 每段独立确定适用额度承运人并计费；总费用为各段之和。
// 所有判定函数均为纯函数，结果只取决于输入，与系统内历史数据规模无关。
package baggage

// ErrCode 拒绝类别，声明顺序即统一拒绝次序。
type ErrCode int

const (
	ErrInvalidParam  ErrCode = iota // 参数非法（航段不连贯、重量为负、件数为零等）
	ErrClockRollback                // 时钟回退
	ErrNotFound                     // 订座记录或旅客不存在
	ErrRecordExists                 // 已有行李记录
	ErrCutoffPassed                 // 已截止
	ErrOverweight                   // 超重拒收（单件超过绝对上限）
	ErrNoRecord                     // 撤销时无行李记录（仅 Cancel 使用）
)

// Error 是系统返回的唯一错误类型，BagSeq 仅在 ErrOverweight 时有意义。
type Error struct {
	Code   ErrCode
	BagSeq int // 第一件超限行李的全局序号（1 起）
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// Policy 承运人额度制式。
type Policy int

const (
	PiecePolicy  Policy = iota // 计件制
	WeightPolicy               // 计重制
)

// Config 系统配置。时间单位与行程时刻一致（分钟），重量单位为百克，费用单位为分。
type Config struct {
	MinConn int64 // 最短中转停留（闭区间下界）
	MaxConn int64 // 最长中转停留（闭区间上界）
	Cutoff  int64 // 托运截止提前量
}

// Airport 机场。
type Airport struct {
	Code    string
	Region  string
	Customs bool // 到达该机场即须提取行李
}

// Carrier 承运人额度与单价。重量单位为百克，费用单位为分。
type Carrier struct {
	Code            string
	Policy          Policy
	FreePieces      int64 // 计件制：免费件数
	PieceFreeWeight int64 // 计件制：单件免费重量上限
	AbsWeight       int64 // 两种制式共用：单件绝对重量上限
	FreeTotalWeight int64 // 计重制：免费总重量
	PieceFee        int64 // 计件制：超件单价（每件）
	OverweightFee   int64 // 计件制：超重单价（每件，与超件可叠加）
	UnitFee         int64 // 计重制：每单位（百克）超重单价
}

// Tier 会员等级提供的额外额度，只用于旅客本人。
type Tier struct {
	Name        string
	ExtraPieces int64 // 计件制额外免费件数
	ExtraWeight int64 // 计重制额外免费重量（百克）
}

// Segment 航段。PNR 标识该航段所属订座记录，用于直挂判定。
type Segment struct {
	From    string
	To      string
	Carrier string
	PNR     string
	Depart  int64
	Arrive  int64
}

// PassengerBags 一名旅客的托运请求。
type PassengerBags struct {
	Passenger string
	Tier      string // 会员等级名，空串表示非会员
	Bags      []int64
}

// Section 一段托运：[Start, End) 为航段下标区间。
type Section struct {
	Start   int
	End     int
	From    string
	To      string
	Carrier string // 本段适用额度承运人
	Fee     int64  // 本段费用（分）
}

// BagTag 一件行李的直挂记录：每段托运一个目的机场。
type BagTag struct {
	Passenger string
	Seq       int
	Dests     []string // 按托运段顺序的直挂终点
}

// Record 办理成功后生成的行李记录。
type Record struct {
	PNR         string
	Itinerary   []Segment
	Sections    []Section
	Tags        []BagTag
	Extractions []string // 必须提取并重新托运的中转站（按行程顺序）
	TotalFee    int64
	Time        int64
}
