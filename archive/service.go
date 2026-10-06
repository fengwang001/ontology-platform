package archive

import "sync"

// Service 是机关档案借阅与预约服务。
//
// 所有业务操作在同一把互斥锁下串行执行，因此并发调用的结果等价于
// 某个串行顺序；操作内部先完成全部校验、后落状态，被拒绝的操作
// 不改变任何状态与时钟。
type Service struct {
	mu sync.Mutex

	cfg Config

	volumes   map[string]*volume
	borrowers map[string]*borrower
	queues    map[string]*queue

	lastNow int
	hasNow  bool
	nextSeq int
}

type volume struct {
	id     string
	level  Level
	status VolumeStatus

	holder    string // 当前持有人（已取走或已分配待取卷）
	loanStart int
	due       int
	renewals  int
	pending   bool // true 表示已自动分配、等待取卷
}

type borrower struct {
	id       string
	maxLevel Level
	status   BorrowerStatus

	accumOverdue  int
	lastReturnDay int
}

type resvEntry struct {
	seq      int
	borrower string
	valid    bool // 已取消/放弃/封存清理则为 false（保留位）

	assigned bool
	offerDay int
	offerDue int
}

type queue struct {
	entries []*resvEntry
}

// New 创建服务。
func New(cfg Config) *Service {
	return &Service{
		cfg:       cfg,
		volumes:   map[string]*volume{},
		borrowers: map[string]*borrower{},
		queues:    map[string]*queue{},
	}
}

// AddVolume / AddBorrower 为管理类接口（不受时钟约束，立即生效）。
func (s *Service) AddVolume(id string, level Level) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return &OpError{Code: ErrInvalidArgument, Index: -1}
	}
	if _, ok := s.volumes[id]; ok {
		return &OpError{Code: ErrInvalidArgument, Index: -1}
	}
	s.volumes[id] = &volume{id: id, level: level, status: InStock}
	s.queues[id] = &queue{}
	return nil
}

func (s *Service) AddBorrower(id string, maxLevel Level) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return &OpError{Code: ErrInvalidArgument, Index: -1}
	}
	if _, ok := s.borrowers[id]; ok {
		return &OpError{Code: ErrInvalidArgument, Index: -1}
	}
	s.borrowers[id] = &borrower{id: id, maxLevel: maxLevel, status: Normal}
	return nil
}

// Borrow 单卷借阅。
func (s *Service) Borrow(now int, borrowerID, volumeID string) error {
	return s.tx(now, func() *OpError {
		return s.borrowOne(now, borrowerID, volumeID)
	})
}

// BorrowBatch 多卷同借，全有或全无；失败时返回最小失败下标。
func (s *Service) BorrowBatch(now int, borrowerID string, volumeIDs []string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Reserve 预约。
func (s *Service) Reserve(now int, borrowerID, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Return 归还。
func (s *Service) Return(now int, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Pickup 取走自动分配给自己的卷。
func (s *Service) Pickup(now int, borrowerID, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Renew 续借：机密及以上需审批人同意，审批人不得是借阅人本人。
func (s *Service) Renew(now int, approverID, borrowerID, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Seal 封存；已借出的卷封存后仍须归还。
func (s *Service) Seal(now int, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Unseal 解封。
func (s *Service) Unseal(now int, volumeID string) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// SetBorrowerLevel 管理操作：调整借阅人最高可借密级。
func (s *Service) SetBorrowerLevel(now int, borrowerID string, level Level) error {
	return s.tx(now, func() *OpError {
		return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// SetBorrowerStatus 管理操作：显式暂停/恢复借阅人。
func (s *Service) SetBorrowerStatus(now int, borrowerID string, status BorrowerStatus) error {
	return s.tx(now, func() *OpError {
	return &OpError{Code: ErrNotFound, Index: -1}
	})
}

// Tick 仅推进时间（触发惰性结算）；时钟非法时报时钟回退。
func (s *Service) Tick(now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasNow && now < s.lastNow {
		return &OpError{Code: ErrClockRollback, Index: -1}
	}
	s.lastNow = now
	s.hasNow = true
	s.settle(now)
	return nil
}

// tx 包裹一个写操作：校验时钟 -> 惰性结算 -> 执行业务。
// 业务返回错误时不得有任何状态变更（各操作在变更前完成全部校验）。
func (s *Service) tx(now int, fn func() *OpError) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasNow && now < s.lastNow {
		return &OpError{Code: ErrClockRollback, Index: -1}
	}
	s.lastNow = now
	s.hasNow = true
	s.settle(now)
	if err := fn(); err != nil {
		return err
	}
	return nil
}

// borrowOne 为单卷借阅的业务实现（下一步填充）。
func (s *Service) borrowOne(now int, borrowerID, volumeID string) *OpError {
	_ = now
	_ = borrowerID
	_ = volumeID
	return &OpError{Code: ErrNotFound, Index: -1}
}

// settle 在当前时刻做惰性结算：过期取卷作废、卷自动分配、暂停解除。
func (s *Service) settle(now int) {
	_ = now
}
