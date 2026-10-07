package scheduler

// Category 为用户类别：免控 / 保底 / 普通。
type Category int

const (
	CatExempt     Category = iota // 免控：任何时候不被限电、不进入通知名单
	CatGuaranteed                 // 保底：被限电时只限到保底功率，通知中须带保底功率
	CatNormal                     // 普通：被限到零
)

// NotifState 为通知生命周期状态。
type NotifState int

const (
	NotifPending   NotifState = iota // 已生成、待确认
	NotifConfirmed                   // 已在时段开始前确认
	NotifWithdrawn                   // 已撤回（改级/取消使该组不再被选）
	NotifAssessed                    // 时段开始时未确认，已记考核
)

// Notification 是某用户在某时段的一条限电通知。同一用户同一时段至多一条。
type Notification struct {
	Slot         int64      // 时段起点
	User         string     // 用户
	Group        string     // 生成通知时用户所在组
	BasePower    int64      // 保底功率（仅保底用户有效）
	HasBasePower bool       // 是否携带保底功率
	State        NotifState // 当前状态
}

// Assessment 是一条「未确认」考核记录。
type Assessment struct {
	Slot  int64
	User  string
	Group string
}

// SlotRecord 记录一个已结算时段的有效等级与被选中的组（按选中顺序）。
type SlotRecord struct {
	Start  int64
	Level  int
	Groups []string
}

// Stats 是内部开销计数器，用于以可验证方式证明性能不变量。
type Stats struct {
	EventsProcessed int64 // 边界结算时实际处理的指令事件数
	LevelHeapOps    int64 // 有效等级堆的压入/弹出次数
	GroupHeapOps    int64 // 选组堆的压入/弹出/调整次数
	UserVisits      int64 // 通知生成过程中访问的用户数
}

type user struct {
	id        string
	group     string
	cat       Category
	basePower int64
}

type group struct {
	id     string
	region string
	users  map[string]bool
}

type region struct {
	id     string
	groups map[string]bool
}

// levelChange 是一次等级变更，eff 为生效的时段边界。
type levelChange struct {
	eff   int64
	level int
}

type instruction struct {
	id        string
	region    string
	start     int64         // 窗口起点（含），对齐时段边界
	end       int64         // 窗口终点（不含），对齐时段边界
	changes   []levelChange // 按 eff 非降序；首条为 {start, 发布等级}
	cancelled bool
	cancelEff int64 // 取消生效的时段边界（cancelled 为真时有效）
}

// levelAt 返回指令在 slotStart 所处时段的等级（调用方保证窗口覆盖该时段）。
func (in *instruction) levelAt(slotStart int64) int {
	lvl := in.changes[0].level
	for _, c := range in.changes {
		if c.eff <= slotStart {
			lvl = c.level
		} else {
			break
		}
	}
	return lvl
}

// activeAt 判断指令在 slotStart 起始的时段是否有效（覆盖且未取消生效）。
func (in *instruction) activeAt(slotStart int64) bool {
	if slotStart < in.start || slotStart >= in.end {
		return false
	}
	if in.cancelled && slotStart >= in.cancelEff {
		return false
	}
	return true
}

type evKind uint8

const (
	evActivate evKind = iota
	evDeactivate
	evSetLevel
	evCancel
)

// instrEvent 是挂在某个时段边界上的指令事件，推进到该边界时处理。
type instrEvent struct {
	id   string
	kind evKind
}

// forecastSlot 是对未来时段的预选结果：有效等级与被选中的组。
type forecastSlot struct {
	level  int
	groups []string
}

// notif 索引：notifs[时段起点][用户]。
type notifIndex map[int64]map[string]*Notification
