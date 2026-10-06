package hd

import "sync"

// System 是血液透析中心机位排程与感染隔离系统的并发安全入口。
// 所有操作互斥串行化，天然等价于某个串行顺序；相同输入序列得到相同分配。
type System struct {
	mu sync.Mutex

	cfg      Config
	clk      clock
	bays     map[string]*Bay
	patients map[string]*Patient

	treatments map[string]*Treatment
	plans      map[string]*Plan
	faults     map[string]*Fault

	byBay     map[string]*baySchedule // 机位 -> 按开始时刻有序的治疗索引
	byPatient map[string]*patientSchedule

	// bayOrder 保留机位登记顺序，编号比较以登记序为字典序（ID 本身非空即可）。
	bayOrder []string
}

// New 创建系统。所有配置时长必须为正整数。
func New(cfg Config) (*System, error) {
	if cfg.CleanNegative <= 0 || cfg.CleanHBV <= 0 || cfg.CleanHCV <= 0 ||
		cfg.DeepClean <= 0 || cfg.MinRecovery <= 0 {
		return nil, errf(ErrInvalidArgument, "all config durations must be positive")
	}
	s := &System{
		cfg:        cfg,
		bays:       map[string]*Bay{},
		patients:   map[string]*Patient{},
		treatments: map[string]*Treatment{},
		plans:      map[string]*Plan{},
		faults:     map[string]*Fault{},
		byBay:      map[string]*baySchedule{},
		byPatient:  map[string]*patientSchedule{},
	}
	return s, nil
}

// 操作接口在后续步骤实现。
