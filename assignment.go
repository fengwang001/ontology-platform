package ontology

// Tier 是一档迟交扣分：迟交时长不超过 MaxLate 时落入该档，扣分比例为 Penalty。
// 档位必须按 MaxLate 递增且 Penalty 递增。
type Tier struct {
	MaxLate int64
	Penalty float64
}

// AssignmentConfig 描述一次作业：统一截止、硬性关闭、迟交档位与评分策略。
type AssignmentConfig struct {
	ID                string
	Deadline          int64 // 统一截止时刻
	HardClose         int64 // 硬性关闭时刻，之后不再接受任何提交
	Tiers             []Tier
	GroupWork         bool // 是否小组作业
	AllowLateOverride bool // 允许迟交覆盖：默认评分版本取全部有效版本中最新者
}

// assignment 是一次作业的全部可变状态。
type assignment struct {
	cfg         AssignmentConfig
	subjects    map[string]*subject // 提交主体：个人作业为学生 ID，小组作业为小组 ID
	groups      map[string]*group
	memberGroup map[string]string // 成员 ID -> 当前所在小组 ID
	grants      map[int64]*grant
	nextGrant   int64
	personal    map[string]*extSet // 个人延期（按成员）
	groupExt    map[string]*extSet // 小组延期（按小组）
	settled     bool
	result      []MemberSettlement
}

func newAssignment(cfg AssignmentConfig) *assignment {
	return &assignment{
		cfg:         cfg,
		subjects:    map[string]*subject{},
		groups:      map[string]*group{},
		memberGroup: map[string]string{},
		grants:      map[int64]*grant{},
		personal:    map[string]*extSet{},
		groupExt:    map[string]*extSet{},
	}
}

// validateConfig 校验作业配置，非法时返回 ErrInvalidArgument。
func validateConfig(op string, cfg AssignmentConfig) error {
	if cfg.ID == "" {
		return newErr(op, ErrInvalidArgument, "assignment id is empty")
	}
	if cfg.Deadline < 0 {
		return newErr(op, ErrInvalidArgument, "deadline %d is negative", cfg.Deadline)
	}
	if cfg.HardClose < cfg.Deadline {
		return newErr(op, ErrInvalidArgument, "hard close %d before deadline %d", cfg.HardClose, cfg.Deadline)
	}
	for i, t := range cfg.Tiers {
		if t.MaxLate < 0 {
			return newErr(op, ErrInvalidArgument, "tier %d has negative max late %d", i, t.MaxLate)
		}
		if t.Penalty < 0 || t.Penalty > 1 {
			return newErr(op, ErrInvalidArgument, "tier %d penalty %v out of [0,1]", i, t.Penalty)
		}
		if i > 0 {
			prev := cfg.Tiers[i-1]
			if t.MaxLate <= prev.MaxLate {
				return newErr(op, ErrInvalidArgument, "tier %d max late %d not increasing after %d", i, t.MaxLate, prev.MaxLate)
			}
			if t.Penalty <= prev.Penalty {
				return newErr(op, ErrInvalidArgument, "tier %d penalty %v not increasing after %v", i, t.Penalty, prev.Penalty)
			}
		}
	}
	return nil
}

// personalMax 返回成员当前个人延期的最大延长时长（无则 0）。
func (a *assignment) personalMax(memberID string) int64 {
	if s, ok := a.personal[memberID]; ok {
		return s.max()
	}
	return 0
}

// groupMax 返回小组当前小组延期的最大延长时长（无则 0）。
func (a *assignment) groupMax(groupID string) int64 {
	if s, ok := a.groupExt[groupID]; ok {
		return s.max()
	}
	return 0
}
