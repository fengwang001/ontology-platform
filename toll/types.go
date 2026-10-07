// Package toll 实现高速公路门架计费的路径还原、结算与补扣服务。
//
// 核心语义：
//   - 行程以入口记录开始、出口记录结束，出口触发结算；
//   - 路径推定在满足依次经过已知门架的所有路径中取总费用最低者，
//     费用相同取门架序列字典序最小者；
//   - 出口后落在 [入口, 出口] 时刻内且在补扣期限内到达的中间记录触发重算，
//     产生补扣或退款；超期记录只登记不重算；区间外的按孤立记录登记；
//   - 同一车辆同一门架同类记录在重复窗口内只保留最早一条；
//   - 每段费用按经过该段起点门架时刻对应的车型费率计算；
//   - 同一车辆同一自然月实收金额受月度封顶约束，退款不得使月实收低于零。
package toll

import (
	"errors"
	"fmt"
	"time"
)

// VehicleClass 车型。
type VehicleClass string

// GantryID 门架标识。
type GantryID string

// VehicleID 车辆标识。
type VehicleID string

// RecordKind 门架记录类型。
type RecordKind int

const (
	KindEntry RecordKind = iota // 入口记录
	KindPass                    // 中间门架记录
	KindExit                    // 出口记录
)

func (k RecordKind) valid() bool { return k >= KindEntry && k <= KindExit }

func (k RecordKind) String() string {
	switch k {
	case KindEntry:
		return "entry"
	case KindPass:
		return "pass"
	case KindExit:
		return "exit"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// RecordStatus 记录登记后的处置状态。
type RecordStatus int

const (
	RecKept        RecordStatus = iota // 有效记录，参与路径推定
	RecDuplicate                       // 重复记录，不触发任何重算
	RecExpiredLate                     // 超过补扣期限的迟到记录，只登记不重算
	RecOrphan                          // 不属于任何行程的孤立记录
)

func (s RecordStatus) String() string {
	switch s {
	case RecKept:
		return "kept"
	case RecDuplicate:
		return "duplicate"
	case RecExpiredLate:
		return "expired-late"
	case RecOrphan:
		return "orphan"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// JourneyStatus 行程状态。
type JourneyStatus int

const (
	JourneyOpen    JourneyStatus = iota // 未结算
	JourneySettled                      // 已结算
	JourneyClosed                       // 人工关闭（始终未结算）
)

func (s JourneyStatus) String() string {
	switch s {
	case JourneyOpen:
		return "open"
	case JourneySettled:
		return "settled"
	case JourneyClosed:
		return "closed"
	}
	return fmt.Sprintf("jstatus(%d)", int(s))
}

// AdjKind 调整类型。
type AdjKind int

const (
	AdjSettle     AdjKind = iota // 出口结算
	AdjBackCharge                // 补扣
	AdjRefund                    // 退款
)

func (k AdjKind) String() string {
	switch k {
	case AdjSettle:
		return "settle"
	case AdjBackCharge:
		return "backcharge"
	case AdjRefund:
		return "refund"
	}
	return fmt.Sprintf("adj(%d)", int(k))
}

// 错误按如下次序判定，一次操作只报次序最靠前的一类。
var (
	ErrInvalidParam      = errors.New("toll: 参数非法")
	ErrClockRollback     = errors.New("toll: 时钟回退")
	ErrGantryNotFound    = errors.New("toll: 门架不存在")
	ErrVehicleNotFound   = errors.New("toll: 车辆不存在")
	ErrJourneyNotFound   = errors.New("toll: 行程不存在")
	ErrJourneySettled    = errors.New("toll: 行程已结算")
	ErrJourneyNotSettled = errors.New("toll: 行程未结算")
	ErrPathUnreachable   = errors.New("toll: 路径不可达")
	ErrExitWithoutEntry  = errors.New("toll: 无入口记录的出口")
)

// Segment 门架之间的有向路段。费用 = Mileage * Rates[车型]（单位：分）。
type Segment struct {
	From, To GantryID
	Mileage  int64
	Rates    map[VehicleClass]int64
}

// Config 服务配置。
type Config struct {
	Classes     []VehicleClass // 合法车型集合
	DedupWindow time.Duration  // 重复窗口：间隔严格小于窗口视为重复
	LateWindow  time.Duration  // 补扣期限：到达时刻距出口时刻不超过该值才触发重算
	MonthlyCap  int64          // 月度封顶（分）
	Location    *time.Location // 自然月切分时区
}

func (c Config) validate() error {
	if len(c.Classes) == 0 || c.DedupWindow <= 0 || c.LateWindow < 0 ||
		c.MonthlyCap < 0 || c.Location == nil {
		return fmt.Errorf("%w: 配置项非法", ErrInvalidParam)
	}
	seen := map[VehicleClass]bool{}
	for _, cl := range c.Classes {
		if cl == "" || seen[cl] {
			return fmt.Errorf("%w: 车型集合非法", ErrInvalidParam)
		}
		seen[cl] = true
	}
	return nil
}

func (c Config) hasClass(cl VehicleClass) bool {
	for _, x := range c.Classes {
		if x == cl {
			return true
		}
	}
	return false
}

// Adjustment 一笔结算/补扣/退款明细，字段足以精确复现该笔调整的依据。
type Adjustment struct {
	Seq            int         // 全局单调序号
	Kind           AdjKind     // 调整类型
	Amount         int64       // 实际收取（结算/补扣）或实际退还（退款）金额
	CappedPart     int64       // 因月度封顶未收取的部分
	UnrefundedPart int64       // 因月实收不得为负而不予退还的部分
	FullAmount     int64       // 本次重算得到的行程全额
	Path           []GantryID  // 本次重算得到的计费路径
	At             time.Time   // 操作时刻
}

// RecordOutcome 描述一条门架记录操作的处置结果与判定依据。
type RecordOutcome struct {
	JourneyID  string
	Status     RecordStatus
	Settled    bool        // 本记录是否触发了一次成功结算
	Path       []GantryID  // 结算/重算后的计费路径
	FullAmount int64       // 结算/重算后的行程全额
	Charged    int64       // 本次实际收取金额（结算时）
	Adjustment *Adjustment // 迟到记录触发的补扣/退款（无则为 nil）
	Note       string      // 判定依据（人类可读）
}

// RecordView 行程/车辆下某条记录的可查询视图。
type RecordView struct {
	Gantry  GantryID
	Kind    RecordKind
	Time    time.Time
	Arrival time.Time
	Status  RecordStatus
}

// JourneyView 行程查询视图。
type JourneyView struct {
	ID                string
	Vehicle           VehicleID
	Status            JourneyStatus
	Path              []GantryID
	FullAmount        int64
	Collected         int64 // 已收金额（净额）
	CappedUncollected int64 // 因封顶未收（待补扣）
	UnrefundedExcess  int64 // 不予退还部分
	Adjustments       []Adjustment
	Records           []RecordView
}

// MonthView 车辆某自然月的汇总视图。
type MonthView struct {
	Month             string
	Collected         int64
	Cap               int64
	CappedUncollected int64
	UnrefundedExcess  int64
}

// OpKind 改变状态的操作类型。
type OpKind int

const (
	OpRegisterVehicle OpKind = iota
	OpChangeClass
	OpAddRecord
	OpCloseJourney
)

// Op 一次改变状态的操作的完整输入，可用于重放。
type Op struct {
	Kind      OpKind
	Vehicle   VehicleID
	Class     VehicleClass // RegisterVehicle / ChangeClass
	Gantry    GantryID     // AddRecord
	RecKind   RecordKind   // AddRecord
	RecTime   time.Time    // AddRecord：记录自身时刻
	Effective time.Time    // ChangeClass：生效时刻
	OpTime    time.Time    // 操作时刻（到达时刻）
	JourneyID string       // CloseJourney
}

func (op Op) String() string {
	switch op.Kind {
	case OpRegisterVehicle:
		return fmt.Sprintf("RegisterVehicle(%s,%s)@%s", op.Vehicle, op.Class, op.OpTime.Format(time.RFC3339))
	case OpChangeClass:
		return fmt.Sprintf("ChangeClass(%s,%s,eff=%s)@%s", op.Vehicle, op.Class,
			op.Effective.Format(time.RFC3339), op.OpTime.Format(time.RFC3339))
	case OpAddRecord:
		return fmt.Sprintf("AddRecord(%s,%s,%s,t=%s)@%s", op.Vehicle, op.Gantry, op.RecKind,
			op.RecTime.Format(time.RFC3339), op.OpTime.Format(time.RFC3339))
	case OpCloseJourney:
		return fmt.Sprintf("CloseJourney(%s)@%s", op.JourneyID, op.OpTime.Format(time.RFC3339))
	}
	return fmt.Sprintf("op(%d)", int(op.Kind))
}
