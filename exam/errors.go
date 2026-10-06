package exam

import "fmt"

// ReasonCode 是拒绝操作的可区分错误类别。
type ReasonCode int

const (
	ReasonOK ReasonCode = iota
	// ReasonInvalidArgument 参数非法（优先级最高）。
	ReasonInvalidArgument
	// ReasonExamAlreadyScheduled 考试已安排。
	ReasonExamAlreadyScheduled
	// ReasonCrossDayOrOutOfRange 时段越界或跨日。
	ReasonCrossDayOrOutOfRange
	// ReasonRoomSlotBusy 考场在某时段已被占用。
	ReasonRoomSlotBusy
	// ReasonRoomCapacity 考场容量之和不足。
	ReasonRoomCapacity
	// ReasonStudentConflict 学生冲突（重叠/超门数/间隔不足）。
	ReasonStudentConflict
	// ReasonStaffShortage 监考人员不足（优先级最低）。
	ReasonStaffShortage
	// ReasonExamNotScheduled 对未安排考试执行撤销/移动。
	ReasonExamNotScheduled
)

// StudentConflictKind 为学生冲突的三种类别，优先级 重叠 > 超门数 > 间隔不足。
type StudentConflictKind int

const (
	StudentConflictOverlap StudentConflictKind = iota
	StudentConflictTooMany
	StudentConflictGap
)

// Error 携带结构化的拒绝信息，便于精确复现归因。
type Error struct {
	Code       ReasonCode
	ExamID     string
	Detail     string
	StudentID  string
	Kind       StudentConflictKind
	ConflictID string // Swap 时与哪门考试冲突导致整体非法
}

func (e *Error) Error() string {
	names := map[ReasonCode]string{
		ReasonInvalidArgument:      "invalid argument",
		ReasonExamAlreadyScheduled: "exam already scheduled",
		ReasonCrossDayOrOutOfRange: "cross-day or out of range",
		ReasonRoomSlotBusy:         "room slot busy",
		ReasonRoomCapacity:         "room capacity insufficient",
		ReasonStudentConflict:      "student conflict",
		ReasonStaffShortage:        "staff shortage",
		ReasonExamNotScheduled:     "exam not scheduled",
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", names[e.Code], e.Detail)
	}
	return names[e.Code]
}
