package railway

import "errors"

// Reason 标识一次被拒绝操作最靠前的一类原因。
type Reason int

const (
	ReasonOK Reason = iota
	ReasonInvalidParam
	ReasonClockRollback
	ReasonTrainNotFound
	ReasonTicketNotFound
	ReasonTicketRefunded
	ReasonDeparted
	ReasonPassengerOverlap
	ReasonQuotaExhausted
	ReasonNoSeat
)

func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonInvalidParam:
		return "invalid param"
	case ReasonClockRollback:
		return "clock rollback"
	case ReasonTrainNotFound:
		return "train not found"
	case ReasonTicketNotFound:
		return "ticket not found"
	case ReasonTicketRefunded:
		return "ticket already refunded"
	case ReasonDeparted:
		return "train departed at origin"
	case ReasonPassengerOverlap:
		return "passenger holds intersecting ticket"
	case ReasonQuotaExhausted:
		return "quota exhausted"
	case ReasonNoSeat:
		return "no seat (standing full)"
	default:
		return "unknown"
	}
}

// OpError 携带统一拒绝类别。Reason 为拒绝次序中最靠前的一类。
type OpError struct {
	Reason Reason
}

func (e *OpError) Error() string { return e.Reason.String() }

func errReason(r Reason) error { return &OpError{Reason: r} }

// 便捷哨兵错误，便于外部 errors.Is。
var (
	ErrInvalidParam     = errors.New(ReasonInvalidParam.String())
	ErrClockRollback    = errors.New(ReasonClockRollback.String())
	ErrTrainNotFound    = errors.New(ReasonTrainNotFound.String())
	ErrTicketNotFound   = errors.New(ReasonTicketNotFound.String())
	ErrTicketRefunded   = errors.New(ReasonTicketRefunded.String())
	ErrDeparted         = errors.New(ReasonDeparted.String())
	ErrPassengerOverlap = errors.New(ReasonPassengerOverlap.String())
	ErrQuotaExhausted   = errors.New(ReasonQuotaExhausted.String())
	ErrNoSeat           = errors.New(ReasonNoSeat.String())
)

// Seat 以车厢号与座位号标识一个物理座位。
type Seat struct {
	Car int
	No  int
}

// TicketDesc 描述一张车票的乘车区段与乘车人。
type TicketDesc struct {
	From      int
	To        int
	Passenger string
}

// Ticket 是一次成功购票产生的记录。
type Ticket struct {
	ID        int64
	TrainID   string
	From      int
	To        int
	Passenger string
	Standing  bool
	Car       int // 无座票为 0
	No        int // 无座票为 0
	Refunded  bool
	// QuotaShared 为 true 表示票额扣自共用票额，否则扣自发站-到站分配票额。
	QuotaShared bool
}

// BuyResult 是购票成功的结论。
type BuyResult struct {
	Ticket *Ticket
}

// QuotaView 是某发站-到站票额在某时刻（并入已提交后）的视图。
type QuotaView struct {
	Allocation int // 该发站该到站尚未并入、尚未售出的分配票额
	Shared     int // 全车共用票额当前余额
	Merged     bool
}
