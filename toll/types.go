// Package toll 实现高速公路门架计费的路径还原、结算与补扣服务。
//
// 错误优先级(每次操作只报次序最靠前的一类):
// 参数非法 > 时钟回退 > 门架不存在 > 车辆不存在 > 行程不存在 >
// 行程已结算 > 行程未结算 > 路径不可达 > 无入口记录的出口。
package toll

import (
	"errors"
	"time"
)

var (
	ErrInvalidParams     = errors.New("参数非法")
	ErrClockRollback     = errors.New("时钟回退")
	ErrGantryNotFound    = errors.New("门架不存在")
	ErrVehicleNotFound   = errors.New("车辆不存在")
	ErrJourneyNotFound   = errors.New("行程不存在")
	ErrJourneySettled    = errors.New("行程已结算")
	ErrJourneyNotSettled = errors.New("行程未结算")
	ErrPathUnreachable   = errors.New("路径不可达")
	ErrExitWithoutEntry  = errors.New("无入口记录的出口")
)

// RecordStatus 是门架记录的处理结果。
type RecordStatus string

const (
	StatusValid     RecordStatus = "VALID"     // 有效记录,参与路径推定
	StatusAccepted  RecordStatus = "ACCEPTED"  // 进入未结算行程
	StatusLate      RecordStatus = "LATE"      // 迟到记录,已触发重算
	StatusDuplicate RecordStatus = "DUPLICATE" // 重复记录,仅登记
	StatusOrphan    RecordStatus = "ORPHAN"    // 不属于任何行程,孤立登记
	StatusExpired   RecordStatus = "EXPIRED"   // 超过补扣期限,仅登记不重算
)

// AdjustKind 是调整类型。
type AdjustKind string

const (
	AdjustCharge     AdjustKind = "CHARGE"     // 出口结算收取
	AdjustBackcharge AdjustKind = "BACKCHARGE" // 迟到记录引发的补扣
	AdjustRefund     AdjustKind = "REFUND"     // 迟到记录引发的退款
)

// Config 是服务配置。
type Config struct {
	DuplicateWindow  time.Duration  // 重复窗口:同车同门架间隔严格小于该值视为重复
	BackchargeWindow time.Duration  // 补扣期限:迟到记录到达时刻距出口时刻超过该值不再重算
	MonthlyCap       int64          // 月度封顶(分),<=0 表示不封顶
	Location         *time.Location // 自然月切分时区,nil 取 UTC
}

// GantryRecord 是一条门架记录。
type GantryRecord struct {
	Gantry string
	Time   time.Time // 记录自身时刻(可乱序到达)
	Seq    int       // 到达序号,用于同时刻记录的确定排序
	Status RecordStatus
}

// Adjustment 是一笔金额调整,携带可复现的依据。
type Adjustment struct {
	Time              time.Time // 操作时刻
	Kind              AdjustKind
	Amount            int64 // 实际收取/退还金额
	CappedUncollected int64 // 因月度封顶未收取的部分
	RefundOverflow    int64 // 退款超出当月实收而不予退还的部分
	FeeBefore         int64
	FeeAfter          int64
	Path              []string // 调整依据的计费路径
	Reason            string   // 人类可读的判定依据
}

// RecordResult 是 Record 操作的结果。
type RecordResult struct {
	Status     RecordStatus
	JourneyID  string
	Adjustment *Adjustment // 触发重算且金额变化时非 nil
	Note       string
}

// ExitResult 是出口结算的结果。
type ExitResult struct {
	JourneyID         string
	Path              []string
	Fee               int64
	Charged           int64
	CappedUncollected int64
}

// JourneyView 是行程查询视图。
type JourneyView struct {
	ID          string
	VehicleID   string
	Settled     bool
	Closed      bool
	Path        []string
	Fee         int64
	Received    int64
	Adjustments []Adjustment
}

// MonthLedger 是某车辆某自然月的台账。
type MonthLedger struct {
	Received          int64 // 当月实收
	CappedUncollected int64 // 因封顶未收(单独可查)
	RefundOverflow    int64 // 退款超出部分不予退还(单独可查)
}

// MonthKey 标识某车辆某自然月。
type MonthKey struct {
	VehicleID string
	Year      int
	Month     time.Month
}
