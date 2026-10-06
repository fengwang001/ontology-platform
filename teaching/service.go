package teaching

import (
	"sort"
	"sync"
)

// assignment 为某教师在某任务上的一条承担关系。
type assignment struct {
	teacherID string
	taskID    string
	hours     int
	status    string
	deadline  int64
	weekStart int // 当前承担段（换人后缩小）
	weekEnd   int
}

// occKey 标识一个不可复用的时段格：周次 × 节次。
type occKey struct {
	week   int
	period int
}

// Service 是线程安全的授课任务分配与工作量核算服务。
// 单一互斥锁串行化所有变更，使并发结果等价于某个串行顺序。
type Service struct {
	mu sync.Mutex

	cfg Config
	now int64

	ranks    map[string]*Rank
	teachers map[string]struct{}
	tasks    map[string]*TaskSpec

	// assignments 以 "teacherID\x00taskID" 为键；同一对只保留最新一条
	// （rejected/released 后可重新指派，旧记录被覆盖）。
	assignments map[string]*assignment

	// occ[teacher][occKey] = 占用该格的任务 ID。
	// 冲突判定为 map 查询，复杂度 O(任务周数×节数)，与历史任务总数无关。
	occ map[string]map[occKey]string

	// 活跃（pending/confirmed）折算工作量累计，含待确认指派。
	load map[string]int

	// 超时堆：惰性释放（containers/heap 不引入外部依赖，手写最小堆）。
	deadlines []*assignment

	// 每任务已分配学时合计（pending+confirmed）。
	allocated map[string]int

	// 冻结：某教师某学期核算完成后，其该学期指派不再允许变更。
	frozen map[string]map[string]struct{}

	// 核算结果与抵扣额度结转。
	settlements map[string]*SettlementResult
	credits     map[string]int // "semester\x00teacher" -> 可用抵扣额度
}

func akey(teacherID, taskID string) string   { return teacherID + "\x00" + taskID }
func ckey(semester, teacherID string) string { return semester + "\x00" + teacherID }

// NewService 用配置与初始时钟构造服务；返回骨架占位实例。
func NewService(cfg Config, now int64) (*Service, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	s := &Service{
		cfg:         cfg,
		now:         now,
		ranks:       map[string]*Rank{},
		teachers:    map[string]struct{}{},
		tasks:       map[string]*TaskSpec{},
		assignments: map[string]*assignment{},
		occ:         map[string]map[occKey]string{},
		load:        map[string]int{},
		allocated:   map[string]int{},
		frozen:      map[string]map[string]struct{}{},
		settlements: map[string]*SettlementResult{},
		credits:     map[string]int{},
	}
	return s, nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Tiers) == 0 {
		return errf(ErrInvalidParameter, "no scale tiers")
	}
	for i := range cfg.Tiers {
		t := cfg.Tiers[i]
		if t.MaxSize <= 0 || t.Coeff <= 0 {
			return errf(ErrInvalidParameter, "tier must have positive max size and coeff")
		}
		if i > 0 && t.MaxSize <= cfg.Tiers[i-1].MaxSize {
			return errf(ErrInvalidParameter, "tiers must be strictly ascending by max size")
		}
	}
	if cfg.NewCourseAdd < 0 || cfg.LabCoeff <= 0 || cfg.ConfirmTicks < 0 {
		return errf(ErrInvalidParameter, "invalid coeff or confirm ticks")
	}
	if len(cfg.Ranks) == 0 {
		return errf(ErrInvalidParameter, "no ranks")
	}
	names := make([]string, 0, len(cfg.Ranks))
	for name := range cfg.Ranks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := cfg.Ranks[name]
		if r.MinLoad < 0 || r.MaxLoad < r.MinLoad {
			return errf(ErrInvalidParameter, "rank %s has invalid bounds", name)
		}
	}
	return nil
}
