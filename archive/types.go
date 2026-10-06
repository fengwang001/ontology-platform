package archive

// Level 为密级，数值越大密级越高，可直接比较。
type Level int

const (
	Public Level = iota // 公开
	Internal            // 内部
	Secret              // 机密
	TopSecret           // 绝密
)

// VolumeStatus 为卷状态。
type VolumeStatus int

const (
	InStock VolumeStatus = iota // 在库
	Lent                        // 借出（含已自动分配、待取卷）
	Sealed                      // 封存
)

// BorrowerStatus 为借阅人状态。Suspended 是显式状态；暂停是否解除
// 取决于“全部归还 + 冷静期”，由服务端惰性判定。
type BorrowerStatus int

const (
	Normal BorrowerStatus = iota
	Suspended
)

// Config 为服务初始化参数。
type Config struct {
	LoanDays     [4]int // 各密级借期天数（按 Public..TopSecret）
	PickupDays   int    // 取卷期限（自分配日起算，含末日）
	RenewWindow  int    // 续借窗口：借期最后一日前固定天数（含末日，起点恰等允许）
	MaxRenewals  int    // 每卷每次借阅的续借次数上限
	OverdueLimit int    // 累计逾期天数达到该阈值进入暂停
	CooldownDays int    // 解除暂停所需的、自最近一次归还起的冷静天数
}

// VolumeView 为卷的对外快照。
type VolumeView struct {
	ID       string
	Level    Level
	Status   VolumeStatus
	Borrower string // 当前借阅人；已分配待取卷时为被分配者；否则为空
	DueDate  int    // 借期最后一日；无在借关系时为 0
	Pending  bool   // true 表示已自动分配、等待取卷
	Renewals int
}

// ReservationView 为预约排队快照（按队列顺序，含已失效保留位）。
type ReservationView struct {
	Seq      int
	Borrower string
	Valid    bool
	Assigned bool
	OfferDay int
	OfferDue int
}

// BorrowerView 为借阅人对外快照。
type BorrowerView struct {
	ID            string
	MaxLevel      Level
	Status        BorrowerStatus
	AccumOverdue  int
	LastReturnDay int
}
