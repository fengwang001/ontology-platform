package archive

// Classification 是档案密级，数值越大密级越高。
type Classification int

const (
	ClassPublic       Classification = iota // 公开
	ClassInternal                           // 内部
	ClassConfidential                       // 机密
	ClassTopSecret                          // 绝密
)

func (c Classification) Valid() bool { return c >= ClassPublic && c <= ClassTopSecret }

func (c Classification) String() string {
	switch c {
	case ClassPublic:
		return "public"
	case ClassInternal:
		return "internal"
	case ClassConfidential:
		return "confidential"
	case ClassTopSecret:
		return "topsecret"
	default:
		return "invalid"
	}
}

// VolumeStatus 是卷的当前状态。
type VolumeStatus int

const (
	VolInLibrary VolumeStatus = iota // 在库
	VolLent                          // 借出（含已自动分配待取）
	VolSealed                        // 封存
)

func (s VolumeStatus) String() string {
	switch s {
	case VolInLibrary:
		return "in_library"
	case VolLent:
		return "lent"
	case VolSealed:
		return "sealed"
	default:
		return "invalid"
	}
}

// UserStatus 是借阅人状态。
type UserStatus int

const (
	UserActive    UserStatus = iota // 正常
	UserSuspended                   // 暂停
)

func (s UserStatus) String() string {
	switch s {
	case UserActive:
		return "active"
	case UserSuspended:
		return "suspended"
	default:
		return "invalid"
	}
}

// Config 是服务初始化参数。所有时长均为整数日。
type Config struct {
	LoanDays           [4]int // 各密级基础借期天数：公开、内部、机密、绝密
	PickupDeadlineDays int    // 被分配者的取卷期限
	RenewWindowDays    int    // 续借窗口固定天数（借期最后一日前的天数，含最后一日）
	MaxRenewals        int    // 每卷每次借阅的续借次数上限
	OverdueThreshold   int    // 累计逾期天数达到该阈值进入暂停
	CooldownDays       int    // 暂停解除冷静天数（自最近一次归还起）
}

// LoanInfo 是一次生效中的借阅。
type LoanInfo struct {
	UserID       string
	StartDay     int
	DueDay       int
	RenewalsUsed int
}

// HoldInfo 是卷已自动分配、等待取走的信息。
type HoldInfo struct {
	UserID      string
	AssignedDay int
	DeadlineDay int
}

// VolumeState 是卷的只读快照。
type VolumeState struct {
	ID           string
	Class        Classification
	Status       VolumeStatus
	Loan         *LoanInfo
	Hold         *HoldInfo
	QueueUserIDs []string
	SealPending  bool
}

// UserState 是借阅人的只读快照。
type UserState struct {
	ID           string
	MaxClass     Classification
	Status       UserStatus
	OverdueTotal int
	LastReturn   int // 最近一次归还日
}

// Outcome 是单次操作的结果。被拒绝时 OK=false，Err 为错误分类，Reason 为判定依据。
type Outcome struct {
	OK     bool
	Err    ErrorCode
	Reason string
}
