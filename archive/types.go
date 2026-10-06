// Package archive 实现机关档案借阅与预约服务。
//
// 时间以整数日序号计。所有操作通过 Apply 进入，由互斥锁串行化，
// 并发调用等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
package archive

import "fmt"

// SecretLevel 密级，由低到高。
type SecretLevel int

const (
	Public       SecretLevel = iota // 公开
	Internal                        // 内部
	Confidential                    // 机密
	TopSecret                       // 绝密
	numLevels
)

func (l SecretLevel) valid() bool { return l >= 0 && l < numLevels }

func (l SecretLevel) String() string {
	switch l {
	case Public:
		return "公开"
	case Internal:
		return "内部"
	case Confidential:
		return "机密"
	case TopSecret:
		return "绝密"
	}
	return fmt.Sprintf("SecretLevel(%d)", int(l))
}

// VolumeStatus 档案卷状态。
type VolumeStatus int

const (
	StatusAvailable VolumeStatus = iota // 在库
	StatusLent                          // 借出
	StatusSealed                        // 封存
)

func (s VolumeStatus) String() string {
	switch s {
	case StatusAvailable:
		return "在库"
	case StatusLent:
		return "借出"
	case StatusSealed:
		return "封存"
	}
	return fmt.Sprintf("VolumeStatus(%d)", int(s))
}

// ErrCode 错误类别。声明顺序即报告优先级（数值小者优先级高）。
type ErrCode int

const (
	ErrInvalidParam  ErrCode = iota + 1 // 参数非法
	ErrClockRollback                    // 时钟回退
	ErrNotFound                         // 卷或借阅人不存在
	ErrInvalidState                     // 状态不允许
	ErrClearance                        // 密级不足
	ErrSuspended                        // 借阅人暂停
	ErrBorrowed                         // 卷已借出
	ErrRenew                            // 续借超限或窗口未到
	ErrReservation                      // 存在有效预约
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "卷或借阅人不存在"
	case ErrInvalidState:
		return "状态不允许"
	case ErrClearance:
		return "密级不足"
	case ErrSuspended:
		return "借阅人暂停"
	case ErrBorrowed:
		return "卷已借出"
	case ErrRenew:
		return "续借超限或窗口未到"
	case ErrReservation:
		return "存在有效预约"
	}
	return fmt.Sprintf("ErrCode(%d)", int(c))
}

// Error 为可区分的操作错误，Msg 给出判定依据。
type Error struct {
	Code  ErrCode
	Index int // 批量操作中失败项的下标；非批量为 -1
	Msg   string
}

func (e *Error) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("%s: [%d] %s", e.Code, e.Index, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func newErr(code ErrCode, index int, format string, args ...any) *Error {
	return &Error{Code: code, Index: index, Msg: fmt.Sprintf(format, args...)}
}

// Config 初始化参数。
type Config struct {
	LoanDays         [4]int // 各密级借期天数（按 Public..TopSecret 顺序）
	PickupDays       int    // 取卷期限天数，自分配日起算
	RenewWindowDays  int    // 续借窗口天数，含借期最后一日
	MaxRenews        int    // 每卷每次借阅的续借次数上限
	OverdueThreshold int    // 累计逾期天数暂停阈值
	CooldownDays     int    // 暂停解除冷静天数
}

// Validate 校验配置合法性。
func (c Config) Validate() error {
	for i, d := range c.LoanDays {
		if d < 0 {
			return fmt.Errorf("LoanDays[%d] 不得为负: %d", i, d)
		}
	}
	if c.PickupDays < 0 {
		return fmt.Errorf("PickupDays 不得为负: %d", c.PickupDays)
	}
	if c.RenewWindowDays < 1 {
		return fmt.Errorf("RenewWindowDays 至少为 1: %d", c.RenewWindowDays)
	}
	if c.MaxRenews < 0 {
		return fmt.Errorf("MaxRenews 不得为负: %d", c.MaxRenews)
	}
	if c.OverdueThreshold < 1 {
		return fmt.Errorf("OverdueThreshold 至少为 1: %d", c.OverdueThreshold)
	}
	if c.CooldownDays < 0 {
		return fmt.Errorf("CooldownDays 不得为负: %d", c.CooldownDays)
	}
	return nil
}

// OpKind 操作类别。
type OpKind int

const (
	OpAddVolume OpKind = iota + 1
	OpAddBorrower
	OpSetBorrowerLevel
	OpSeal
	OpBorrow
	OpReserve
	OpReturn
	OpRenew
	OpPickup
)

func (k OpKind) String() string {
	switch k {
	case OpAddVolume:
		return "AddVolume"
	case OpAddBorrower:
		return "AddBorrower"
	case OpSetBorrowerLevel:
		return "SetBorrowerLevel"
	case OpSeal:
		return "Seal"
	case OpBorrow:
		return "Borrow"
	case OpReserve:
		return "Reserve"
	case OpReturn:
		return "Return"
	case OpRenew:
		return "Renew"
	case OpPickup:
		return "Pickup"
	}
	return fmt.Sprintf("OpKind(%d)", int(k))
}

// Op 为一次操作的完整输入。
type Op struct {
	Kind       OpKind
	Now        int
	BorrowerID string
	VolumeID   string
	VolumeIDs  []string // 批量借阅
	Level      SecretLevel
	ApproverID string // 续借审批人
}

// BorrowResult 单卷借阅结果。
type BorrowResult struct {
	VolumeID string
	DueDay   int
}

// Result 为一次操作的输出。Err 非 nil 表示被拒绝，其余字段无意义。
type Result struct {
	Err           *Error
	BorrowResults []BorrowResult // Borrow 成功时
	OverdueDays   int            // Return 成功时
	DueDay        int            // Pickup 成功时的借期最后一日
	NewDueDay     int            // Renew 成功时
}

// VolumeSnapshot 卷的可观察状态。
type VolumeSnapshot struct {
	ID         string
	Level      SecretLevel
	Status     VolumeStatus
	Loan       *LoanSnapshot
	Assignment *AssignmentSnapshot
	Queue      []string // 等待中的预约借阅人，按排队顺序
}

// LoanSnapshot 在借信息。
type LoanSnapshot struct {
	BorrowerID string
	BorrowDay  int
	DueDay     int
	Renews     int
}

// AssignmentSnapshot 待取卷分配。
type AssignmentSnapshot struct {
	BorrowerID string
	AssignDay  int
	Deadline   int
}

// BorrowerSnapshot 借阅人可观察状态（给定 now 下的有效值）。
type BorrowerSnapshot struct {
	ID            string
	MaxLevel      SecretLevel
	Suspended     bool // 有效状态（含冷静期推导）
	CumOverdue    int  // 有效累计逾期天数（解除后清零）
	ActiveLoans   int
	LastReturn    int
	HasLastReturn bool
}

// StateSnapshot 全量可观察状态，用于确定性比对。
type StateSnapshot struct {
	Volumes   []VolumeSnapshot
	Borrowers []BorrowerSnapshot
	Watermark int
	HasClock  bool
}

// Stats 运行统计，用于验证分配扫描开销。
type Stats struct {
	QueueScanSteps int64 // 归还/期满重分配时扫描过的预约节点总数
}
