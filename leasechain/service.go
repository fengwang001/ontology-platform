package leasechain

import (
	"errors"
	"fmt"
	"sync"
)

// 固定次序错误：所有操作只返回下列分类中的第一个错误。
var (
	ErrInvalidParam     = errors.New("参数非法")
	ErrClockRollback    = errors.New("时钟回退")
	ErrLeaseUnavailable = errors.New("租约不存在或已终止")
	ErrNoConsent        = errors.New("未取得同意")
	ErrTermOutOfRange   = errors.New("期限越出上级")
	ErrRentTooHigh      = errors.New("租金超过倍数上限")
	ErrDepthExceeded    = errors.New("链深度超限")
	ErrIllegalState     = errors.New("状态不允许该操作")
)

// Config 为全局配置：P 为租金倍数（整数百分比，如 100 表示不得超过上级租金），
// D 为链深度上限（主租约为 0 级，深度 D 的次租约允许，D+1 拒绝），
// G 为逾期宽限天数，PayDay 为每月固定付款日序（1..28）。
type Config struct {
	P      int
	D      int
	G      int
	PayDay int
}

// Lease 表示一份租约。ParentID 为 0 表示与房东的直接租约（主租约或提升后的租约）。
type Lease struct {
	ID         int64
	LandlordID string
	TenantID   string
	ParentID   int64
	Start      int
	End        int
	Rent       int64
	Recognized bool
	Terminated bool
	ChildID    int64 // 0 表示无有效下级
}

// Arrear 为一笔欠费（对应一个账期）。
type Arrear struct {
	ID      int64
	LeaseID int64
	Due     int
	Amount  int64
	Paid    int64
}

// Recourse 为一次清偿形成的一笔追偿权。
type Recourse struct {
	ID       int64
	ArrearID int64
	PayerID  int64 // 实际清偿的租约（对欠费租约的承租人享有追偿权）
	Amount   int64
}

// OneTimeConsent 为一次性房东同意，绑定一份确定的拟转租条款。
type OneTimeConsent struct {
	ID         int64
	LandlordID string
	ParentID   int64
	TenantID   string
	Start      int
	End        int
	Rent       int64
	GrantAt    int64
	Used       bool
}

// generalConsent 为概括性同意的最近一次授予/撤回状态（撤回时间 0 表示有效）。
type generalConsent struct {
	landlord  string
	tenant    string
	grantedAt int64
	revokedAt int64
}

// bill 为一个已生成应付账期。
type bill struct {
	leaseID int64
	due     int
	amount  int64
}

// Service 为转租链管理服务。所有方法可并发调用，内部以单一互斥保证可串行化。
type Service struct {
	mu        sync.Mutex
	cfg       Config
	lastNow   int
	clock     int64 // 单调逻辑时钟，用于同意授予/撤回时间戳
	nextID    int64
	leases    map[int64]*Lease
	arrears   map[int64]*Arrear
	recourses map[int64]*Recourse
	oneTimes  []*OneTimeConsent
	generals  map[string]generalConsent
	bills     map[billKey]*bill
}

type billKey struct {
	leaseID int64
	due     int
}

// New 创建服务。配置非法返回 ErrInvalidParam。
func New(cfg Config) (*Service, error) {
	if cfg.P < 0 || cfg.D < 0 || cfg.G < 0 || cfg.PayDay < 1 || cfg.PayDay > 28 {
		return nil, ErrInvalidParam
	}
	return &Service{
		cfg:       cfg,
		leases:    map[int64]*Lease{},
		arrears:   map[int64]*Arrear{},
		recourses: map[int64]*Recourse{},
		generals:  map[string]generalConsent{},
		bills:     map[billKey]*bill{},
	}, nil
}

func wrapErr(base error, detail string) error { return fmt.Errorf("%w: %s", base, detail) }

func cloneLease(l *Lease) *Lease {
	cp := *l
	return &cp
}

// CreateMaster 由房东与承租人签订主租约。
func (s *Service) CreateMaster(now int, landlord, tenant string, start, end int, rent int64) (*Lease, error) {
	if landlord == "" || tenant == "" || start >= end || rent <= 0 {
		return nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out *Lease
	err := s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		tx.nextID++
		l := &Lease{
			ID:         tx.nextID,
			LandlordID: landlord,
			TenantID:   tenant,
			Start:      start,
			End:        end,
			Rent:       rent,
		}
		tx.leases[l.ID] = l
		out = cloneLease(l)
		return nil
	})
	return out, err
}

// Advance 将时钟推进到 now，并结算到期账期与到期租约。
func (s *Service) Advance(now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		return nil
	})
}

func (s *Service) checkClockLocked(now int) error {
	if now < s.lastNow {
		return ErrClockRollback
	}
	return nil
}

// acceptClockLocked 在一个操作通过全部校验、即将生效时推进时钟与逻辑时钟。
func (s *Service) acceptClockLocked(now int) {
	if now > s.lastNow {
		s.clock++
	}
	s.lastNow = now
}

// GetLease 查询租约快照（不修改时钟与状态）。
func (s *Service) GetLease(id int64) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.leases[id]
	if l == nil {
		return nil, ErrLeaseUnavailable
	}
	return cloneLease(l), nil
}

// GetArrear 查询欠费快照（不修改时钟与状态）。
func (s *Service) GetArrear(id int64) (*Arrear, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.arrears[id]
	if a == nil {
		return nil, ErrLeaseUnavailable
	}
	cp := *a
	return &cp, nil
}

// settleLocked 处理截至 now 的账期生成、欠费产生与租约到期级联。
// 顺序固定：先账期、再欠费、最后到期级联，保证重放确定性。
func (s *Service) settleLocked(now int) {
	s.generateBillsLocked(now)
	s.createArrearsLocked(now)
	s.expireLeasesLocked(now)
}
