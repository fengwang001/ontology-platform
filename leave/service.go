package leave

import (
	"sync"
	"sync/atomic"
)

const daysPerYear = 365

// maxSpanDays 限制单张假单的最大跨度，防止恶意超长区间造成单次操作退化为
// 全表扫描；200 年对年假场景已足够宽松。
const maxSpanDays = 200 * daysPerYear

// Config 是服务的初始化配置。
type Config struct {
	// NonWorkdays 是年内日偏移（0..364）集合，对每个年度同样生效。
	NonWorkdays []int
	// TenureBounds 是工龄档位边界（单位：年，严格递增、非负）。
	// 工龄恰等于边界时归入高档（取等归高）。
	TenureBounds []int
	// AnnualQuotas 是各档年额度，长度必须等于 len(TenureBounds)+1。
	AnnualQuotas []int
	// CarryCap 是单个年度结转上限。
	CarryCap int
	// CarryDeadline 是结转额度在次年可用的截止日（年内日偏移，0..364）。
	CarryDeadline int
}

func (c *Config) validate() error {
	if len(c.AnnualQuotas) == 0 || len(c.AnnualQuotas) != len(c.TenureBounds)+1 {
		return errf(CatInvalidParam, "annual quotas length must equal tenure bounds length + 1")
	}
	for i, b := range c.TenureBounds {
		if b < 0 {
			return errf(CatInvalidParam, "tenure bound %d is negative", b)
		}
		if i > 0 && b <= c.TenureBounds[i-1] {
			return errf(CatInvalidParam, "tenure bounds must be strictly increasing")
		}
	}
	for _, q := range c.AnnualQuotas {
		if q < 0 {
			return errf(CatInvalidParam, "annual quota %d is negative", q)
		}
	}
	if c.CarryCap < 0 {
		return errf(CatInvalidParam, "carry cap %d is negative", c.CarryCap)
	}
	if c.CarryDeadline < 0 || c.CarryDeadline >= daysPerYear {
		return errf(CatInvalidParam, "carry deadline %d out of range [0,364]", c.CarryDeadline)
	}
	for _, d := range c.NonWorkdays {
		if d < 0 || d >= daysPerYear {
			return errf(CatInvalidParam, "non-workday offset %d out of range [0,364]", d)
		}
	}
	return nil
}

// Source 标记一笔扣减的额度来源。
type Source int

const (
	// Current 当年额度。
	Current Source = iota
	// Carried 上年度结转额度。
	Carried
)

func (s Source) String() string {
	if s == Carried {
		return "carried"
	}
	return "current"
}

// Charge 是一个计扣日的扣减记录，也是回补去向的依据。
type Charge struct {
	Day    int
	Year   int
	Source Source
}

// Status 是假单状态。
type Status int

const (
	StatusPending   Status = iota // 已提交待批准（占用额度）
	StatusApproved                // 已批准（确认使用）
	StatusRejected                // 已驳回（终结）
	StatusWithdrawn               // 已撤回（终结）
	StatusCancelled               // 已整单销假（终结）
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusApproved:
		return "approved"
	case StatusRejected:
		return "rejected"
	case StatusWithdrawn:
		return "withdrawn"
	case StatusCancelled:
		return "cancelled"
	}
	return "unknown"
}

// Balance 是某一年度在某时刻的余额视图，区分四个量：
// 当年额度 / 结转额度 × 占用中 / 已确认使用。
type Balance struct {
	Year             int
	CurrentGranted   int // 当年额度发放量
	CurrentPending   int // 当年额度占用中（待批准）
	CurrentUsed      int // 当年额度已确认使用
	CurrentAvailable int // 当年额度可用 = 发放 - 占用 - 已用 - 已结转出去
	CarriedGranted   int // 结转额度发放量
	CarriedPending   int // 结转额度占用中
	CarriedUsed      int // 结转额度已确认使用
	CarriedAvailable int // 结转额度可用 = 发放 - 占用 - 已用 - 已作废
}

// ledger 是单个年度的可变动台账（发放量由配置与入职日确定性推出，不入账）。
type ledger struct {
	usedCur    int
	usedCar    int
	pendCur    int
	pendCar    int
	voidedCar  int  // 回补到已过期结转而作废的量
	carriedOut int  // 已结转到下一年度的量（结转钉死后不再变化）
	pinned     bool // carriedOut 是否已钉死
}

// histEntry 是某年度台账在某次被接受操作后的快照，按 now 递增追加。
type histEntry struct {
	now int
	val ledger
}

// yearAcct 是单个年度的当前台账 + 变更历史（历史查询的数据基础）。
type yearAcct struct {
	cur  ledger
	hist []histEntry
}

// chargeRec 是假单内部保存的逐日扣减记录。
type chargeRec struct {
	day     int
	carried bool
}

type leaveRec struct {
	id       int64
	from, to int
	status   Status
	charges  []chargeRec
}

type employee struct {
	mu        sync.Mutex
	hireDay   int
	regNow    int
	carryYear int // 结转已钉死到的年度（含）；初始为入职年
	years     map[int]*yearAcct
	leaves    map[int64]*leaveRec
	intervals itreap
}

// Service 是年休假额度账户服务。所有方法可并发调用，
// 结果等价于某个串行顺序。
type Service struct {
	cfg     Config
	nonWork [daysPerYear]bool
	mu      sync.RWMutex // 保护 emps
	emps    map[string]*employee
	clock   atomic.Int64 // 上一次被接受操作的 now，初始 -1
	nextID  atomic.Int64
}

// NewService 校验配置并创建服务。
func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, emps: make(map[string]*employee)}
	for _, d := range cfg.NonWorkdays {
		s.nonWork[d] = true
	}
	s.clock.Store(-1)
	s.nextID.Store(0)
	return s, nil
}

func yearOf(day int) int { return day / daysPerYear }
func doyOf(day int) int  { return day % daysPerYear }

// checkClock 只做只读校验；时钟只在操作被接受时推进。
func (s *Service) checkClock(now int) *Error {
	if int64(now) < s.clock.Load() {
		return errf(CatClockRegression, "now=%d is before last accepted now=%d", now, s.clock.Load())
	}
	return nil
}

// commitClock 将时钟推进到 now（只增不减）。
func (s *Service) commitClock(now int) {
	for {
		cur := s.clock.Load()
		if int64(now) <= cur {
			return
		}
		if s.clock.CompareAndSwap(cur, int64(now)) {
			return
		}
	}
}

func (s *Service) getEmployee(id string) *employee {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.emps[id]
}

// tenureYears 返回年度 y 首日的累计工龄（整年，向下取整）。
// 仅对 y > 入职年 调用，保证被减数为正。
func (e *employee) tenureYears(y int) int {
	return (daysPerYear*y - e.hireDay) / daysPerYear
}

// tier 返回工龄所属档位，边界取等归高档。
func (s *Service) tier(tenure int) int {
	t := 0
	for _, b := range s.cfg.TenureBounds {
		if tenure >= b {
			t++
		}
	}
	return t
}

// quota 返回年度 y 的当年额度发放量，是配置与入职日的确定性函数。
func (s *Service) quota(e *employee, y int) int {
	hireYear := yearOf(e.hireDay)
	switch {
	case y < hireYear:
		return 0
	case y == hireYear:
		// 入职当年：按入职当日（含）至年底的剩余日数占全年比例折算，
		// 不足一日舍去；折算后不再受工龄档影响。
		remaining := daysPerYear - doyOf(e.hireDay)
		base := s.cfg.AnnualQuotas[s.tier(0)]
		return base * remaining / daysPerYear
	default:
		return s.cfg.AnnualQuotas[s.tier(e.tenureYears(y))]
	}
}

func (e *employee) acct(y int) *yearAcct {
	a, ok := e.years[y]
	if !ok {
		a = &yearAcct{}
		e.years[y] = a
	}
	return a
}

func (e *employee) acctRO(y int) *yearAcct {
	if a, ok := e.years[y]; ok {
		return a
	}
	return nil
}

// deriveCarry 计算从年度 y-1 结转到年度 y 的量：
// min(上年度当年额度未用量, 结转上限)。未用量中扣除占用中（计入已用）。
func (s *Service) deriveCarry(e *employee, y int) int {
	prev := e.acctRO(y - 1)
	var used, pend int
	if prev != nil {
		used, pend = prev.cur.usedCur, prev.cur.pendCur
	}
	unused := s.quota(e, y-1) - used - pend
	if unused < 0 {
		unused = 0
	}
	if unused > s.cfg.CarryCap {
		return s.cfg.CarryCap
	}
	return unused
}

// carryOf 返回年度 y 的结转额度（只读，不钉死）：
// 已钉死则取钉死值；否则按当前台账推导（与钉死结果一致）。
func (s *Service) carryOf(e *employee, y int) int {
	if prev := e.acctRO(y - 1); prev != nil && prev.cur.pinned {
		return prev.cur.carriedOut
	}
	return s.deriveCarry(e, y)
}

// pinCarries 把 (carryYear, nowYear] 各年度的结转依次钉死，
// 等价于逐年触达。只在被接受的操作上调用。
func (s *Service) pinCarries(e *employee, nowYear, now int) {
	for y := e.carryYear + 1; y <= nowYear; y++ {
		co := s.deriveCarry(e, y)
		prev := e.acct(y - 1)
		prev.cur.carriedOut = co
		prev.cur.pinned = true
		e.touch(y-1, now)
	}
	if nowYear > e.carryYear {
		e.carryYear = nowYear
	}
}

// touch 把年度 y 当前台账追加一条历史快照。
func (e *employee) touch(y, now int) {
	a := e.acct(y)
	a.hist = append(a.hist, histEntry{now: now, val: a.cur})
}

// Register 登记员工及其入职日。
func (s *Service) Register(now int, empID string, hireDay int) error {
	if now < 0 {
		return errf(CatInvalidParam, "now=%d is negative", now)
	}
	if empID == "" {
		return errf(CatInvalidParam, "empty employee id")
	}
	if hireDay < 0 {
		return errf(CatInvalidParam, "hire day %d is negative", hireDay)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.emps[empID]; ok {
		return errf(CatInvalidState, "employee %q already registered", empID)
	}
	s.emps[empID] = &employee{
		hireDay:   hireDay,
		regNow:    now,
		carryYear: yearOf(hireDay),
		years:     make(map[int]*yearAcct),
		leaves:    make(map[int64]*leaveRec),
	}
	s.commitClock(now)
	return nil
}
