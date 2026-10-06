package maintenance

import "sync"

// Service 为物业维修派单服务。所有方法并发安全，等价于某种串行顺序。
type Service struct {
	mu sync.RWMutex

	cfg     Config
	lastNow int
	hasNow  bool

	contractors map[int]*contractor
	orders      map[int]*order

	contractorIDs  []int
	orderIDs       []int
	nextOrderID    int
	nextContractor int

	queue  orderQueue
	events []Event
}

// New 创建服务。时限必须为正，R 必须至少为 1。
func New(cfg Config) *Service {
	return &Service{
		cfg:         cfg,
		contractors: map[int]*contractor{},
		orders:      map[int]*order{},
	}
}

// advanceLocked 校验时钟单调并推进；调用方须持有 mu（写锁）。
func (s *Service) advanceLocked(now int) error {
	if s.hasNow && now < s.lastNow {
		return ErrClockBackward
	}
	s.lastNow = now
	s.hasNow = true
	return nil
}

func (s *Service) emitLocked(at int, typ EventType, orderID, contractorID int, level Level, reason string) {
	s.events = append(s.events, Event{
		Seq:          len(s.events) + 1,
		At:           at,
		Type:         typ,
		OrderID:      orderID,
		ContractorID: contractorID,
		NewLevel:     level,
		Reason:       reason,
	})
}

// Level 为工单紧急等级，1 最低，4 为紧急。
type Level uint8

const (
	LevelRoutine   Level = 1
	LevelElevated  Level = 2
	LevelHigh      Level = 3
	LevelEmergency Level = 4
)

func (l Level) valid() bool { return l >= LevelRoutine && l <= LevelEmergency }

// Status 为工单的持久状态。逾期是已确认状态下的子状态。
type Status uint8

const (
	StatusQueued Status = iota + 1
	StatusDispatched
	StatusConfirmed
	StatusOverdue
	StatusCompleted
	StatusCancelled
)

func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "queued"
	case StatusDispatched:
		return "dispatched"
	case StatusConfirmed:
		return "confirmed"
	case StatusOverdue:
		return "overdue"
	case StatusCompleted:
		return "completed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Config 为四个等级各自的响应/完成时限，以及累计拒单多少次后自动升级。
type Config struct {
	ResponseLimit [4]int
	CompleteLimit [4]int
	RejectUpgrade int
}

func (c Config) responseLimit(l Level) int { return c.ResponseLimit[l-1] }
func (c Config) completeLimit(l Level) int { return c.CompleteLimit[l-1] }

// EventType 列举系统产生的确定性事件。
type EventType string

const (
	EventDispatched EventType = "dispatched"
	EventRejected   EventType = "rejected"
	EventPreempted  EventType = "preempted"
	EventUpgraded   EventType = "upgraded"
	EventOverdue    EventType = "overdue"
	EventReturned   EventType = "returned"
)

// Event 为一次可观测事件。
type Event struct {
	Seq          int
	At           int
	Type         EventType
	OrderID      int
	ContractorID int
	NewLevel     Level
	Reason       string
}

// OrderView 为查询返回的工单快照（按给定 now 反映应有状态）。
type OrderView struct {
	ID            int
	Tenant        string
	Trade         string
	Building      string
	Level         Level
	SubmittedAt   int
	Status        Status
	AssignedTo    int
	DispatchedAt  int
	ConfirmedAt   int
	ResponseDue   int
	CompletionDue int
	RejectCount   int
}

// order 为工单内部实体。
type order struct {
	id          int
	tenant      string
	trade       string
	building    string
	level       Level
	submittedAt int

	status       Status
	assignedTo   int
	dispatchedAt int
	confirmedAt  int
	responseDue  int
	completeDue  int

	rejectCount    int
	rejectedBy     map[int]struct{}
	overdueEmitted bool
}

// contractor 为承包商内部实体。
type contractor struct {
	id               int
	trades           map[string]struct{}
	buildings        map[string]struct{}
	capacity         int
	acceptsEmergency bool
	active           bool
	completedBefore  bool
	lastCompletion   int
	// holds 为全部在手工单（含已确认与逾期），决定容量占用。
	holds map[int]struct{}
	// pending 为其中尚未确认的工单，仅它可被抢占；已确认即从中删除。
	pending map[int]struct{}
}

// activeOrders 返回在手且未撤销的工单数；holds 在完成/撤销时同步删除，
// 因此它就是当前在手数，独立于历史拒单/超时回队。
func (s *Service) contractorLoad(c *contractor) int { return len(c.holds) }

func (s *Service) full(c *contractor) bool { return len(c.holds) >= c.capacity }
