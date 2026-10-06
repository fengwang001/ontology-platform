package repair

type Level int

const (
	Level1 Level = iota
	Level2
	Level3
	Urgent
)

type Limits struct {
	Response   int
	Completion int
}

type Config struct {
	Limits      [4]Limits
	RejectLimit int
}

type TicketStatus int

const (
	StatusQueued TicketStatus = iota
	StatusAssigned
	StatusConfirmed
	StatusOverdue
	StatusCompleted
	StatusCanceled
)

type Ticket struct {
	ID             int64
	TenantID       int64
	Trade          string
	Building       string
	Level          Level
	SubmittedAt    int
	LevelStartedAt int
	Status         TicketStatus
	Assignee       int64
	DispatchedAt   int
	ConfirmedAt    int
	ResponseDue    int
	CompleteDue    int
	Rejections     int
	RejectedBy     []int64
}

type Contractor struct {
	ID              int64
	Trades          map[string]bool
	Buildings       map[string]bool
	Capacity        int
	AcceptsUrgent   bool
	Inactive        bool
	ActiveCount     int
	LastCompletedAt int
}

type Event struct {
	At           int
	TicketID     int64
	ContractorID int64
	Kind         EventKind
	FromLevel    Level
	ToLevel      Level
}

type ticket struct {
	id             int64
	tenantID       int64
	trade          string
	building       string
	level          Level
	submittedAt    int
	levelStartedAt int
	status         TicketStatus
	assignee       int64
	dispatchedAt   int
	confirmedAt    int
	responseDue    int
	completeDue    int
	rejections     int
	rejectedBy     map[int64]bool
	completedAt    int
}

type contractor struct {
	id              int64
	trades          map[string]bool
	buildings       map[string]bool
	capacity        int
	acceptsUrgent   bool
	inactive        bool
	registration    int
	lastCompletedAt int
	active          map[int64]*ticket
	completion      *timerHeap
}

type event struct {
	at           int
	ticketID     int64
	contractorID int64
	kind         EventKind
	fromLevel    Level
	toLevel      Level
}

type EventKind int

const (
	EventAssigned EventKind = iota + 1
	EventConfirmed
	EventRejected
	EventRequeued
	EventUpgraded
	EventResponseOverdue
	EventCompletionOverdue
	EventPreempted
	EventCompleted
	EventCanceled
)
