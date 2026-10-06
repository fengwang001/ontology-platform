package settlement

// system.go —— 核心系统：账户、营业日、并发控制。

import "sync"

// Config 为系统配置。
type Config struct {
	// BusinessDays 为有序（不要求连续）营业日集合。
	BusinessDays []int64
	// MaxFailDays 为累计失败营业日数阈值 B，达到后强制了结。
	MaxFailDays int
	// PenaltyBPS 为罚率基点（除以 10000）。
	PenaltyBPS int64
}

// System 是日终批处理与失败处理系统。
// 所有方法可并发调用，语义等价于某种串行顺序。
type System struct {
	mu sync.Mutex

	bizDays map[int64]int // day -> 下标
	days    []int64
	maxFail int
	penBPS  int64

	accounts map[string]*account
	orders   map[int64]*instruction

	lastBatchDay int64 // 最近一次已完成批处理的营业日；0 表示尚未开始
	lastReport   BatchReport
	// carry 是尚未了结指令的有序集合，只含应交割日不晚于最近批处理日、
	// 或未来应交割的指令；按 (应交割日, 编号) 组织，单日批处理只扫描当日应扫描部分。
	byDay map[int64][]*instruction // 应交割日 -> 指令（登记时按编号插入有序）
	// overdue 为应交割日早于/等于当前批处理日但仍未了结的滚动指令，按编号升序。
	overdue []*instruction
}

type account struct {
	name       string
	securities map[int64]int64
	cash       int64
	feePay     int64
	feeRecv    int64
	compPay    int64
	compRecv   int64
}

type instruction struct {
	Order
	delivered int64
	failDays  int
	status    Status
}

// NewSystem 构造系统；配置非法返回 ErrInvalidParam。
func NewSystem(cfg Config) (*System, error) {
	if cfg.MaxFailDays <= 0 || cfg.PenaltyBPS < 0 || len(cfg.BusinessDays) == 0 {
		return nil, ErrInvalidParam
	}
	days := make([]int64, len(cfg.BusinessDays))
	copy(days, cfg.BusinessDays)
	sortInt64s(days)
	biz := make(map[int64]int, len(days))
	for i, d := range days {
		if i > 0 && days[i-1] == d {
			return nil, ErrInvalidParam // 营业日重复
		}
		biz[d] = i
	}
	return &System{
		bizDays:  biz,
		days:     days,
		maxFail:  cfg.MaxFailDays,
		penBPS:   cfg.PenaltyBPS,
		accounts: make(map[string]*account),
		orders:   make(map[int64]*instruction),
		byDay:    make(map[int64][]*instruction),
	}, nil
}

func sortInt64s(x []int64) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

// AddAccount 登记一个账户及其初始头寸。
func (s *System) AddAccount(name string, securities map[int64]int64, cash int64) error {
	if name == "" || cash < 0 {
		return ErrInvalidParam
	}
	secs := make(map[int64]int64, len(securities))
	for k, v := range securities {
		if v < 0 {
			return ErrInvalidParam
		}
		secs[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[name]; ok {
		return ErrInvalidParam // 账户重名按参数非法处理
	}
	s.accounts[name] = &account{name: name, securities: secs, cash: cash}
	return nil
}
