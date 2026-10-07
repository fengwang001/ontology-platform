package settlement

import "sort"

// Tier 为一个迟交扣分档位：迟交时长不超过 MaxLate 时扣 PenaltyBPS 个基点。
// 档位之间按时长上限递增、扣分比例递增（在创建时强校验）。
type Tier struct {
	MaxLate    int
	PenaltyBPS int
}

// Config 描述一个作业的静态配置。
//
// Deadline 为统一截止时刻；HardClose 为硬性关闭时刻（关闭后不再接受任何
// 提交，恰等于 HardClose 的提交仍然接受）。IsGroup 为 true 时该作业为
// 小组作业，提交主体是小组，扣分仍按成员分别结算。
// Tiers 按首个“上限不小于迟交时长”的档位取扣分；超过最后一档上限的
// 版本记为无效版本。PenaltyBPS 采用基点（10000 = 100%），避免浮点误差。
type Config struct {
	Deadline  int
	HardClose int
	Tiers     []Tier
	IsGroup   bool
	// AllowLateOverride 为 true 时评分版本取全部有效版本中最新者，
	// 并按该版本档位扣分；false（默认）时取准时版本中最新者。
	AllowLateOverride bool
}

func (c Config) validate() *ClassifiedError {
	if c.Deadline < 0 || c.HardClose < c.Deadline {
		return classified(ErrInvalidParam, "deadline/hardclose invalid")
	}
	if len(c.Tiers) == 0 {
		return classified(ErrInvalidParam, "no penalty tiers")
	}
	prevLate := 0
	prevPen := 0
	for i, t := range c.Tiers {
		if t.MaxLate <= 0 || t.PenaltyBPS < 0 || t.PenaltyBPS > 10000 {
			return classified(ErrInvalidParam, "tier bounds invalid")
		}
		if i > 0 && (t.MaxLate <= prevLate || t.PenaltyBPS <= prevPen) {
			return classified(ErrInvalidParam, "tiers must be strictly increasing")
		}
		prevLate, prevPen = t.MaxLate, t.PenaltyBPS
	}
	return nil
}

// penaltyFor 返回迟交时长 late 落入的扣分基点与“是否超过最后一档上限”。
// 判定采用二分，复杂度 O(log |Tiers|)，仅依赖档位数，不随提交/延期/
// 成员等任何其他规模增长（见 DESIGN.md 的开销证明）。
func (c Config) penaltyFor(late int) (bps int, tooLate bool) {
	if late <= 0 {
		return 0, false
	}
	i := sort.Search(len(c.Tiers), func(i int) bool { return c.Tiers[i].MaxLate >= late })
	if i == len(c.Tiers) {
		return 0, true
	}
	return c.Tiers[i].PenaltyBPS, false
}

// Person 保存一名学生在一个作业内的状态与其个人延期索引。
type Person struct {
	id       string
	personal extIndex
	// groupID 非空且 inGroup 为 true 时表示当前在组；退出后 groupID 保留
	// 用于结算定位，但 inGroup 为 false。
	groupID string
	inGroup bool
	// 退出快照：退出时刻与当时小组最新版本号（无版本则为 0）。
	leftAt     int
	leftSubjNo int
}

// Group 保存一个小组在一个作业内的成员与版本。
type Group struct {
	id       string
	members  map[string]bool
	versions []*Version
	// designate 为小组主体当前显式指定的评分版本号（0 表示默认规则）。
	designate int
}

// Assignment 保存一个作业的全部可变状态。
//
// 个人作业：每人在 ownVersions 中拥有独立提交主体；个人延期只进该人的
// personal 索引。小组作业：versions 在 Group 上，小组延期进 groupExt，
// 成员个人延期仍在 Person.personal，只影响该成员取档。
type Assignment struct {
	cfg Config

	people map[string]*Person
	groups map[string]*Group

	// 个人作业：personID -> 该个人主体的版本序列。
	ownVersions map[string][]*Version
	// 个人主体显式指定的评分版本号（0 表示默认规则）。
	ownDesignate map[string]int

	// 小组延期：groupID -> 对全体成员生效的延期索引。
	groupExt map[string]*extIndex

	// 每个成员在任一时刻可见延期的缓存，仅用于 CreateAssignment 之后的
	// 便捷查询；真实结算以版本快照为准（见 submission.go）。
	nextExtID  int
	settled    bool
	settlement map[string]*MemberResult
}

// CreateAssignment 创建作业。id 必须非空且未被使用；作业必须带有严格
// 递增的扣分档位与 Deadline <= HardClose。时刻 now 用于全局时钟校验。
func (e *Engine) CreateAssignment(id string, cfg Config, now int) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.assignments[id]; ok {
		return classified(ErrInvalidParam, "assignment id already exists")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	e.assignments[id] = &Assignment{
		cfg:          cfg,
		people:       map[string]*Person{},
		groups:       map[string]*Group{},
		ownVersions:  map[string][]*Version{},
		ownDesignate: map[string]int{},
		groupExt:     map[string]*extIndex{},
		nextExtID:    1,
	}
	return nil
}

// AddPerson 把学生登记到作业中。必须在创建小组或任何提交之前完成。
func (e *Engine) AddPerson(assignmentID, personID string, now int) error {
	if personID == "" {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.assignments[assignmentID]
	if !ok {
		return classified(ErrNotFound, "assignment not found: "+assignmentID)
	}
	if _, dup := a.people[personID]; dup {
		return classified(ErrInvalidParam, "person already exists: "+personID)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	a.people[personID] = &Person{id: personID, personal: *newExtIndex()}
	return nil
}

func (e *Engine) lookupAssignment(id string) (*Assignment, *ClassifiedError) {
	a, ok := e.assignments[id]
	if !ok {
		return nil, classified(ErrNotFound, "assignment not found: "+id)
	}
	return a, nil
}
