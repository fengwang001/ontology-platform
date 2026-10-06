// Package scheduler 实现考场排考与调考引擎。
//
// 时段模型：按日划分，每日若干连续时段；全局编号 slot = day*slotsPerDay +
// index，唯一且单调。考试占用左闭右闭的整时段区间，可跨相邻时段但不可跨日。
//
// 时长与加时：允许加时时长 = 标准 + ceil(标准*比例)，不足一段向上进位；
// 整场考试取最长者，容纳延长学生的考场在延长段同样被占用（考场按标识
// 升序、延长学生优先入座，规则确定）。
//
// 学生约束：区间不相交、同日门数不超上限、同日相邻考试空闲段不少于下限
// （恰等于下限合法）；多条同时成立只报 重叠 > 超门数 > 间隔不足，
// 同类报最小学生标识。拒绝优先级：非法参数 > 已安排 > 跨日越界 >
// 考场占用 > 容量不足 > 学生冲突 > 监考不足；被拒绝操作零写入。
//
// 操作：Schedule/Move/Swap/Cancel 与 StudentSlots/RoomSlots/ProctorSlots；
// 全部在同一互斥锁内校验并提交，并发等价于某串行顺序。详见 DESIGN.md。
package scheduler

// types.go 为整个包提供公共类型定义。

// Config 描述排考引擎的全局配置。
type Config struct {
	SlotsPerDay           int     // 每日连续时段数
	Days                  int     // 日历天数
	MaxExamsPerStudent    int     // 同一学生同日考试门数上限
	MinGapSlots           int     // 同一学生相邻考试之间至少空闲的时段数
	ExtensionRatio        float64 // 加时比例（按标准时长乘以此比例计算延长时段）
	MaxProctorSlotsPerDay int     // 每人每日监考时段总数上限
}

// ExtensionRatio 已移入 Config 字段。

// Exam 描述一门待安排考试的静态信息。
type Exam struct {
	ID               string
	Students         []string // 参加考试的学生标识
	ExtendedStudents []string // 其中需要延长时间的学生标识
	StandardSlots    int      // 标准占用时段数
	AllowExtension   bool     // 是否允许加时
}

// Room 描述一个考场。
type Room struct {
	ID       string
	Capacity int
}

// Proctor 描述一名监考人员。
type Proctor struct {
	ID string
}

// RoomAssign 描述考试在单个考场的分配。
type RoomAssign struct {
	RoomID       string
	CapacityUsed int
	Extended     bool // 该考场是否容纳需要延长时间的学生（占用延长时段）
}

// Placement 描述一门考试被接受后的安排结果。
type Placement struct {
	StartSlot int
	EndSlot   int
	Rooms     []RoomAssign
	Proctors  map[int]map[string]string // 时段 -> 考场 -> 监考人员
}

// ReasonCode 为错误原因的稳定枚举。
type ReasonCode string

const (
	ReasonInvalidParam         ReasonCode = "invalid_param"
	ReasonExamAlreadyPlaced    ReasonCode = "exam_already_placed"
	ReasonExamNotPlaced        ReasonCode = "exam_not_placed"
	ReasonCrossDayOrOutOfRange ReasonCode = "slot_cross_day_or_out_of_range"
	ReasonRoomSlotBusy         ReasonCode = "room_slot_busy"
	ReasonCapacityInsufficient ReasonCode = "capacity_insufficient"
	ReasonStudentConflict      ReasonCode = "student_conflict"
	ReasonProctorInsufficient  ReasonCode = "proctor_insufficient"
)

// ConflictCategory 为学生冲突的三种类别。
type ConflictCategory string

const (
	ConflictOverlap     ConflictCategory = "overlap"
	ConflictOverCount   ConflictCategory = "over_count"
	ConflictGapTooSmall ConflictCategory = "gap_too_small"
)

// SchedError 携带稳定、可区分的拒绝原因。
type SchedError struct {
	Code     ReasonCode
	Message  string
	Student  string
	Category ConflictCategory
	ExamID   string
}

func (e *SchedError) Error() string { return string(e.Code) + ": " + e.Message }

// Occupy 表示某个主体在某时段被占用的一条记录。
type Occupy struct {
	Slot   int
	Day    int
	ExamID string
	RoomID string
}
