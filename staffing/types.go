package staffing

// PositionSpec 描述岗位：职级带宽（两端含）与编制总数。
type PositionSpec struct {
	ID        string
	BandLow   int
	BandHigh  int
	Headcount int
}

// Position 是岗位的运行时视图。
type Position struct {
	ID        string
	BandLow   int
	BandHigh  int
	Headcount int
	Frozen    bool
}

// OfferStatus 为通知生命周期状态。
type OfferStatus int

const (
	StatusPending OfferStatus = iota
	StatusAccepted
	StatusOnboarded
	StatusRejected
	StatusExpired
	StatusWithdrawn
	StatusCanceled
	StatusAbandoned
)

func (s OfferStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusAccepted:
		return "ACCEPTED"
	case StatusOnboarded:
		return "ONBOARDED"
	case StatusRejected:
		return "REJECTED"
	case StatusExpired:
		return "EXPIRED"
	case StatusWithdrawn:
		return "WITHDRAWN"
	case StatusCanceled:
		return "CANCELED"
	case StatusAbandoned:
		return "ABANDONED"
	default:
		return "UNKNOWN"
	}
}

// Offer 是录用通知的完整记录。
type Offer struct {
	ID            int64
	CandidateID   string
	PositionID    string
	Salary        int
	Deadline      int
	IssuedAt      int
	Status        OfferStatus
	RespondedAt   int
	EntryDate     int
	OnboardedAt   int
	LeftAt        int // 离职日；未离职为 -1
	CanceledAt    int // 协商取消日；未取消为 -1
	UsedException bool
}

// ExceptionApproval 是某岗位某季度（90 日一季，自日序号 0 起）的例外额度。
type ExceptionApproval struct {
	ID         string
	PositionID string
	Quarter    int
	Total      int
	Remaining  int
}

// BatchItem 是批量发放中的一份通知。
type BatchItem struct {
	CandidateID string
	PositionID  string
	Salary      int
	Deadline    int
}

// Snapshot 是用于断言与模型对照的完整状态视图。
type Snapshot struct {
	Now        int
	Positions  map[string]Position
	Candidates map[string]bool
	Offers     map[int64]Offer
	Exceptions map[string]ExceptionApproval
	Occupied   map[string]int
	Onboarded  map[string]int
	Pending    map[string]int
}

// StepLog 记录一步操作的输入、输出与判定依据。
type StepLog struct {
	Seq    int
	Op     string
	Input  string
	OK     bool
	Code   Code
	Output string
	Reason string
}

// QuarterOf 返回日序号 day 所属季度：day/90 向下取整。
func QuarterOf(day int) int {
	return day / 90
}
