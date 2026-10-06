package deposit

import (
	"fmt"
	"sync"
)

// Category 扣项类别，固定受偿次序由 Rank 决定。
type Category int

const (
	Rent     Category = iota // 欠租
	Damage                   // 损坏
	Cleaning                 // 清洁
	Other                    // 其他
)

func (c Category) valid() bool { return c >= Rent && c <= Other }

// Config 押金服务的时限与违约金配置。
// 违约金按每日 rateNum/rateDen（固定比例）计算并向下取整。
type Config struct {
	A       int   // 申报期：退房日起 A 天内（第 A 天当天可申报）
	B       int   // 争议期：申报期结束后 B 天内
	C       int   // 法定退还期：起算日起 C 天内（第 C 天当天退还免责）
	RateNum int64 // 每日违约金比例分子
	RateDen int64 // 每日违约金比例分母（必须为正）
}

func (c Config) valid() bool {
	return c.A >= 0 && c.B >= 0 && c.C >= 0 && c.RateNum >= 0 && c.RateDen > 0
}

// claimStatus 扣项生命周期状态。
type claimStatus int

const (
	csActive    claimStatus = iota // 有效（申报后未撤销、未争议或已裁定）
	csWithdrawn                    // 已在申报期内撤销
	csDisputed                     // 争议中、冻结中
	csAdjudged                     // 已裁定终结
)

// Claim 一条扣项的运行时记录。
type Claim struct {
	ID       int
	Category Category
	Amount   int64
	FiledAt  int
	Seq      int // 申报先后序号（同类别内受偿次序）

	Status claimStatus
	// Paid 为该扣项在申报期截止时按固定次序确定的押金受偿金额。
	// 撤销、争议与裁定均不改变其他扣项的 Paid；本字段即 O(1) 重算的依据。
	Paid int64
	// Vested 为 Paid 中已归属房东的金额（未争议部分在争议期结束后归属；
	// 裁定支持部分在裁定日归属）。
	Vested         int64
	AdjudgedAmount int64 // 裁定金额；状态为 csAdjudged 时有意义
}

// tranche 一笔带独立退还时限的应退租户款项。
type tranche struct {
	Amount   int64
	Start    int // 时限起算日（申报期结束日或裁定日）
	Refunded bool
	RefundAt int
}

// lease 每份租约的全部状态。
type lease struct {
	id       string
	deposit  int64
	checkout int

	claims map[int]*Claim
	order  []*Claim // 按（类别, 申报序号）固定受偿次序
	nextID int

	allocated bool // 申报期结束时是否已锁定受偿

	tranches []*tranche // 尚未退清与已退的应退款项
	refunded int64      // 已退还租户总额（含已退 tranche）

	events []Record
}

// Service 押金退还与扣减争议处理服务。所有方法可并发调用。
//
// 时钟：s.now 为上一次被接受操作的时间，任何 now < s.now 的调用立即报
// ErrClockRollback 且不改变任何状态；被接受后 s.now 单调推进。
// 所有公共方法持有 s.mu，因此并发调用等价于某个串行顺序。
type Service struct {
	mu     sync.Mutex
	cfg    Config
	now    int // 上一次被接受操作的时钟
	leases map[string]*lease
	log    func(string)
}

const maxAmount int64 = 1 << 60

func newService(cfg Config, logger func(string)) *Service {
	if !cfg.valid() {
		panic("deposit: invalid config")
	}
	return &Service{cfg: cfg, leases: map[string]*lease{}, log: logger}
}

func (s *Service) emit(format string, args ...any) {
	if s.log != nil {
		s.log(fmt.Sprintf(format, args...))
	}
}

func (s *Service) record(l *lease, op string, now int, args map[string]int64) {
	l.events = append(l.events, Record{Op: op, Now: now, Args: args})
}

// enter 执行公共操作前的通用前置校验，按规范固定次序报告第一个错误：
// 参数非法 -> 时钟回退 -> 租约不存在/未退房。
// 通过后返回租约；调用方持锁。
func (s *Service) enter(now int, leaseID string, needsLease bool) (*lease, error) {
	if leaseID == "" {
		return nil, errf(ErrIllegalArgument, "empty lease id")
	}
	if now < s.now {
		return nil, errf(ErrClockRollback, "now=%d < last=%d", now, s.now)
	}
	l, ok := s.leases[leaseID]
	if needsLease && (!ok || l.checkout < 0) {
		return nil, errf(ErrNoLease, "lease %q", leaseID)
	}
	return l, nil
}

// settle 在读取/操作前把惰性状态推进到 now：申报期截止则锁定受偿，
// 争议期截止则归属无争议扣项。幂等且仅依赖已锁定数据。
func (s *Service) settle(l *lease, now int) {
	if !l.allocated && now > l.checkout+s.cfg.A {
		s.allocate(l, now)
	}
	s.matureVested(l, now)
}

// View 返回某租约在 now 时点的对账快照（只读，不推进任何金额，仅惰性归属）。
// 守恒恒等式：Refunded + Vested + Frozen + Pending == Deposit。
func (s *Service) View(now int, leaseID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return Snapshot{}, err
	}
	s.settle(l, now)

	snap := Snapshot{Deposit: l.deposit, Refunded: l.refunded}
	for _, c := range l.claims {
		if c.Status == csWithdrawn {
			continue
		}
		if c.Status == csDisputed {
			snap.Frozen += c.Paid
		}
		snap.Vested += c.Vested
		switch c.Status {
		case csAdjudged:
			snap.Receivable += c.AdjudgedAmount - c.Vested
		default:
			snap.Receivable += c.Amount - c.Paid
		}
	}
	for _, t := range l.tranches {
		if !t.Refunded {
			snap.Refundable += t.Amount
			snap.Liability += s.liabilityFor(t, now)
		}
	}
	snap.Pending = l.deposit - l.refunded - snap.Vested - snap.Frozen

	claims := make([]Claim, 0, len(l.claims))
	for _, c := range l.claims {
		claims = append(claims, *c)
	}
	snap.Claims = claims
	return snap, nil
}

// Snapshot 供查询/对账使用的只读视图。
type Snapshot struct {
	Deposit    int64
	Refunded   int64
	Vested     int64
	Frozen     int64
	Pending    int64
	Receivable int64
	Liability  int64
	Refundable int64
	Claims     []Claim
}

// Record 一次被接受操作的留痕（被拒绝的操作不留痕）。
type Record struct {
	Op   string
	Now  int
	Args map[string]int64
}
