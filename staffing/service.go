package staffing

import "sync"

// Service 是岗位编制与录用通知管理服务。
// 全部公开方法可并发调用；内部以单一互斥锁串行化，
// 因此任意并发交错都等价于某个合法串行顺序，不可能超占用。
type Service struct {
	mu sync.Mutex

	now      int
	clockSet bool

	positions  map[string]*Position
	candidates map[string]bool
	offers     map[int64]*Offer
	exceptions map[string]*ExceptionApproval // key: positionID + "/" + quarter
	// exceptionByKey: (positionID,quarter) -> approval，发放时 O(1) 定位。
	exceptionByKey map[string]*ExceptionApproval

	// 占用索引使发放判定为 O(1)，不随历史通知总数增长：
	// onboardedCnt 在岗人数；pendingCnt 未决通知数（PENDING + ACCEPTED）。
	onboardedCnt map[string]int
	pendingCnt   map[string]int
	// pendingByPos 记录岗位当前全部未决通知 ID，供整岗惰性结算使用；
	// 其大小等于未决数而非历史总数，不破坏发放判定的 O(1) 开销。
	pendingByPos map[string]map[int64]bool

	// candidatePending 记录候选人当前未决（PENDING/ACCEPTED）通知 ID。
	candidatePending map[string]int64
	// candidateOnboarded 记录候选人当前在岗通知 ID（用于离职）。
	candidateOnboarded map[string]int64
	// lastBlock 记录候选人在某岗位最近一次拒绝/放弃日期（冷却依据）。
	lastBlock map[string]map[string]int

	nextOfferID int64

	// cooldown 为拒绝/放弃冷却天数；grace 为入职宽限天数。
	cooldown int
	grace    int

	logger func(StepLog)
	seq    int
}

// New 创建服务。cooldown 为拒绝/放弃冷却天数，grace 为入职宽限天数。
func New(cooldown, grace int) *Service {
	if cooldown < 0 {
		cooldown = 0
	}
	if grace < 0 {
		grace = 0
	}
	return &Service{
		positions:          map[string]*Position{},
		candidates:         map[string]bool{},
		offers:             map[int64]*Offer{},
		exceptions:         map[string]*ExceptionApproval{},
		exceptionByKey:     map[string]*ExceptionApproval{},
		onboardedCnt:       map[string]int{},
		pendingCnt:         map[string]int{},
		pendingByPos:       map[string]map[int64]bool{},
		candidatePending:   map[string]int64{},
		candidateOnboarded: map[string]int64{},
		lastBlock:          map[string]map[string]int{},
		cooldown:           cooldown,
		grace:              grace,
	}
}

// SetLogger 安装逐步日志钩子（每步输入、输出、判定依据）。
func (s *Service) SetLogger(f func(StepLog)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = f
}
