package submission

import "sort"

// Tier 迟交扣分档位：迟交时长不超过 MaxLate（含）时扣分比例为 Penalty。
// 档位须按时长上限与扣分比例同时严格递增。
type Tier struct {
	MaxLate int
	Penalty float64
}

// AssignmentConfig 作业配置。
type AssignmentConfig struct {
	ID                string
	Deadline          int // 统一截止时刻
	HardClose         int // 硬性关闭时刻，之后不再接受任何提交
	Tiers             []Tier
	GroupMode         bool // 小组作业 / 个人作业
	AllowLateOverride bool // 允许迟交覆盖：默认评分版本可取最新有效版本
}

// Assignment 作业聚合：截止与档位、版本、延期、小组归属、结算结果。
type Assignment struct {
	cfg AssignmentConfig

	settled     bool
	firstSubmit bool

	versions map[string][]*Version // 提交主体 -> 版本序列（版本号连续无洞）
	explicit map[string]int        // 提交主体 -> 显式指定的评分版本号

	personalExts map[string][]*Extension // 学生 -> 其全部个人延期
	groupExts    map[string][]*Extension // 小组 -> 其全部小组延期
	personalMax  map[string]int          // 学生 -> 当前生效个人延期的最大延长时长（O(1) 查询缓存）
	groupMax     map[string]int          // 小组 -> 当前生效小组延期的最大延长时长

	groups       map[string]map[string]bool // 小组 -> 成员集合
	studentGroup map[string]string          // 学生 -> 所在小组
	leavers      map[string]LeaveRecord     // 已退出成员 -> 退出时刻快照

	settlements []Settlement
}

func newAssignment(cfg AssignmentConfig) *Assignment {
	return &Assignment{
		cfg:          cfg,
		versions:     map[string][]*Version{},
		explicit:     map[string]int{},
		personalExts: map[string][]*Extension{},
		groupExts:    map[string][]*Extension{},
		personalMax:  map[string]int{},
		groupMax:     map[string]int{},
		groups:       map[string]map[string]bool{},
		studentGroup: map[string]string{},
		leavers:      map[string]LeaveRecord{},
	}
}

// effectiveDeadline 返回某成员当前可见的有效截止时刻：
// 统一截止 + max(个人生效延期, 小组生效延期)。只读两次 map，开销 O(1)，
// 与该作业的延期总数无关。
func (a *Assignment) effectiveDeadline(studentID string) int {
	extra := a.personalMax[studentID]
	if g, ok := a.studentGroup[studentID]; ok {
		if gm := a.groupMax[g]; gm > extra {
			extra = gm
		}
	}
	return a.cfg.Deadline + extra
}

// lookupTier 二分查找首个「时长上限不小于 late」的档位，
// 开销 O(log 档位数)，不随版本数、延期数、成员数等任何其他规模增长。
func lookupTier(tiers []Tier, late int) (Tier, bool) {
	i := sort.Search(len(tiers), func(i int) bool { return tiers[i].MaxLate >= late })
	if i == len(tiers) {
		return Tier{}, false
	}
	return tiers[i], true
}
