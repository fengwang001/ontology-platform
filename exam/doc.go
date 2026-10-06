// Package exam 实现考场排考与调考引擎。
//
// 组装顺序：先用 NewCalendar 定义“日 × 每日时段”网格，再向 VenueRegistry
// 登记考场容量、向 Enrollment 登记考试（学生集合、标准时长、加时比例）、
// 向 StaffRegistry 登记监考人员，最后用 NewEngine 装配全部约束与配置。
//
// 对外操作（均并发安全、可线性化）：
//
//   - ScheduleExam(examID, startSlot, roomIDs)：安排；拒绝时状态不变。
//   - Move(examID, startSlot, roomIDs)：整体移动，以“自身已移除”为基准判定。
//   - Swap(examA, examB)：原子互换两门考试的时段与考场组合。
//   - Cancel(examID)：释放全部占用（含延长时段与监考人员）。
//   - QueryByStudent / QueryByRoom / QueryByStaff：按时段升序返回占用。
//
// 错误一律为 *exam.Error，其 Code 为固定的拒绝类别，StudentID/Kind 给出
// 学生冲突的首个学生与类别（重叠/超门数/间隔不足），ExamID/ConflictID 用于
// 移动与互换的归因。详细取舍见 DESIGN.md。
package exam
