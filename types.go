// Package baggage 实现联程行李中转直挂判定与超重/超件计费。
package baggage

import "errors"

// Weight 以百克为单位的非负整数重量。
type Weight int

// Money 以分为单位的非负整数金额。
type Money int64

// Minute 自纪元起的分钟数，用于起飞/办理/时刻比较。
type Minute int64

// Region 机场所属区域。
type Region string

// Tier 会员等级。
type Tier string

// AllowanceMode 承运人额度制式。
type AllowanceMode int

const (
	// ModePiece 计件制。
	ModePiece AllowanceMode = iota + 1
	// ModeWeight 计重制。
	ModeWeight
)

// Airport 机场：归属区域，是否到达即清关。
type Airport struct {
	Code    string
	Region  Region
	Customs bool
}

// Carrier 承运人额度制式与单价。
type Carrier struct {
	ID   string
	Mode AllowanceMode

	// 计件制。
	FreePieces      int    // 每人免费件数
	PieceFreeWeight Weight // 单件免费重量上限
	// 两种制式共用的单件绝对重量上限。
	AbsWeight Weight

	// 计重制。
	FreeWeight Weight // 每人免费总重量

	ExtraPieceRate Money // 计件制：超件单价（每件）
	OverweightRate Money // 计件制：单件超重单价（每件一次）
	PerUnitRate    Money // 计重制：每单位（百克）超重单价
}

// Segment 一条航段。
type Segment struct {
	CarrierID string
	PNR       string
	From      string
	To        string
	DepartsAt Minute
	ArrivesAt Minute
}

// PartyBags 同一订座记录内一名旅客的交运行李（件数为零非法）。
type PartyBags struct {
	PNR         string
	PassengerID string
	Weights     []Weight
}

// CheckInRequest 办理托运请求。
type CheckInRequest struct {
	Itinerary []Segment
	Parties   []PartyBags
	At        Minute
}

// Config 系统级配置。
type Config struct {
	MinConnect      int64           // 最短中转停留（分钟，含端点）
	MaxConnect      int64           // 最长中转停留（分钟，含端点）
	CutoffLead      int64           // 截止提前量（分钟）
	TierExtraPiece  map[Tier]int    // 会员额外：计件制加件数
	TierExtraWeight map[Tier]Weight // 会员额外：计重制加重量
}

// ConsignmentInfo 一次托运（两次提取之间）的计费结果。
type ConsignmentInfo struct {
	Index     int
	From      string
	To        string
	PNR       string
	CarrierID string
	Mode      AllowanceMode
	Fee       Money
}

// BagInfo 单件行李的直挂终点与提取点。
type BagInfo struct {
	Index        int
	Weight       Weight
	Party        int
	FinalAirport string
	ClaimPoints  []string
}

// Record 办理成功后生成的行李记录。
type Record struct {
	ID           string
	Itinerary    []Segment
	Consignments []ConsignmentInfo
	Bags         []BagInfo
	TotalFee     Money
	CheckedInAt  Minute
}

// 统一拒绝原因，拒绝次序见 CheckIn。
var (
	// ErrInvalid 参数非法（航段不连贯、重量为负、件数为零等）。
	ErrInvalid = errors.New("baggage: invalid parameter")
	// ErrClockRewind 时钟回退。
	ErrClockRewind = errors.New("baggage: clock rewind")
	// ErrNotFound 订座记录或旅客不存在。
	ErrNotFound = errors.New("baggage: pnr or passenger not found")
	// ErrExistingRecord 已有行李记录。
	ErrExistingRecord = errors.New("baggage: baggage record already exists")
	// ErrCutoff 已截止。
	ErrCutoff = errors.New("baggage: check-in closed")
)

// OverweightRejectedError 超重拒收：记录第一件超限行李的全局序号（1 起）。
type OverweightRejectedError struct {
	BagIndex int
}

func (e *OverweightRejectedError) Error() string { return "baggage: overweight rejected" }
