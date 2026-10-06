// Package congestion 实现多层嵌套收费区的拥堵收费与豁免结算服务。
//
// 核心模型：
//   - 区域按“完全包含或不相交”组织为嵌套结构；进入内层同时视为进入全部外层。
//   - 自然日按配置时区切分；仅收费时段（左闭右开）内当日首次进入产生计费。
//   - 资格优先级 残障 > 新能源 > 居民；居民折扣按最小货币单位向上取整，日封顶在折扣之后。
//   - 追溯登记在追溯天数内对历史日重算，差额作为带符号调整（退款为负）入账。
//   - 争议冻结当日应付，期间变更只记录不入账；关闭时一次性重算并记一笔差额。
//   - 全部状态变更经单一互斥锁串行化；操作时刻单调，回退即拒绝且不留任何副作用。
//
// 可复现性：每车每日保存逐行计费依据 ChargeLine 与逐笔调整 Adjustment，
// 恒有 Payable == Σ Adjustment.Amount；相同操作序列重放结果完全一致。
package congestion

import "time"

// 错误按校验次序分类，调用方可用 errors.Is 区分。
var (
	ErrInvalid         = &kindError{"invalid parameter"}
	ErrClockBack       = &kindError{"clock moved backwards"}
	ErrZoneNotFound    = &kindError{"zone not found"}
	ErrVehicleNotFound = &kindError{"vehicle not found"}
	ErrZonesOverlap    = &kindError{"zones intersect but neither contains the other"}
	ErrIntervalOverlap = &kindError{"entitlement interval overlaps an existing one of the same kind"}
	ErrTooLate         = &kindError{"retroactive registration beyond the allowed window"}
	ErrDisputeExists   = &kindError{"dispute already open for this vehicle and day"}
	ErrNoDispute       = &kindError{"no open dispute for this vehicle and day"}
)

type kindError struct{ msg string }

func (e *kindError) Error() string { return e.msg }

// Kind 返回错误类别，无错误返回空串。
func Kind(err error) string {
	switch err {
	case ErrInvalid:
		return "invalid"
	case ErrClockBack:
		return "clock_back"
	case ErrZoneNotFound:
		return "zone_not_found"
	case ErrVehicleNotFound:
		return "vehicle_not_found"
	case ErrZonesOverlap:
		return "zones_overlap"
	case ErrIntervalOverlap:
		return "interval_overlap"
	case ErrTooLate:
		return "retrograde_too_late"
	case ErrDisputeExists:
		return "dispute_exists"
	case ErrNoDispute:
		return "no_dispute"
	}
	return ""
}

// QualificationKind 为资格类别，优先级由 priority() 决定（残障 > 新能源 > 居民）。
type QualificationKind int

const (
	Resident QualificationKind = iota
	Disabled
	NewEnergy
)

func (k QualificationKind) valid() bool { return k >= Resident && k <= NewEnergy }

// Config 为全局结算配置。金额一律为最小货币单位的整数。
type Config struct {
	Location      *time.Location
	DailyCap      int64
	RetroDays     int
	DiscountBasis int64 // 居民折扣减免比例的分母，如 10000 表示万分比
}

// Zone 收费区域。Cells 为区域覆盖的离散网格单元集合，用于判定嵌套与相交。
type Zone struct {
	ID        string
	DailyFee  int64
	Cells     map[string]bool
	StartHHMM int // 收费时段起（本地时间，含），分钟数
	EndHHMM   int // 收费时段止（本地时间，不含），分钟数，须大于 StartHHMM（不跨夜）
}

// Qualification 一辆车持有的一段生效区间内的资格（左闭右开）。
type Qualification struct {
	Kind       QualificationKind
	ZoneID     string // 仅居民折扣使用：登记的那一个区域
	Discount   int64  // 仅居民折扣使用：减免万分比 [0,DiscountBasis]
	Start, End time.Time
}

// RawEntry 一条进入事件的存档信息。
type RawEntry struct {
	Seq       int64
	VehicleID string
	ZoneID    string
	At        time.Time
	Plate     string // 归车时的车牌（含变更边界语义）
}

// ChargeLine 重算产出的逐笔计费依据，仅含在收费时段内、当日首次的进入。
type ChargeLine struct {
	Seq     int64
	ZoneID  string
	At      time.Time
	Gross   int64 // 区域日费
	Payable int64 // 折扣/豁免/封顶后本次实际计入
	Reason  string
}

// Adjustment 台账中的每一笔调整（退款为负、补收为正）。
type Adjustment struct {
	Seq    int64
	At     time.Time
	Amount int64
	Reason string
	Before int64
	After  int64
}

// DayReport 查询结果：某车某日的应付与全部依据。
type DayReport struct {
	VehicleID   string
	Day         string // YYYY-MM-DD（配置时区）
	Payable     int64
	Lines       []ChargeLine
	Adjustments []Adjustment
	Frozen      bool
}
