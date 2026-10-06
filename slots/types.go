// Package slots 实现机场起降时刻协调与历史优先权系统。
package slots

import "fmt"

// Reason 拒绝类别，声明顺序即统一拒绝次序（靠前者优先报告）。
type Reason int

const (
	ReasonInvalidParam      Reason = iota // 参数非法
	ReasonClockRollback                   // 时钟回退
	ReasonNotFound                        // 公司或系列不存在
	ReasonDeadlinePassed                  // 申请已截止（航季阶段不符）
	ReasonSeasonSettled                   // 航季已结算（航季阶段不符）
	ReasonSwapExecuted                    // 周已执行不可交换（航季阶段不符）
	ReasonPhaseMismatch                   // 其他航季阶段不符
	ReasonRegisterLate                    // 登记超期
	ReasonDuplicateRegister               // 重复登记
	ReasonCapacity                        // 容量不足
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidParam:
		return "参数非法"
	case ReasonClockRollback:
		return "时钟回退"
	case ReasonNotFound:
		return "公司或系列不存在"
	case ReasonDeadlinePassed:
		return "申请已截止"
	case ReasonSeasonSettled:
		return "航季已结算"
	case ReasonSwapExecuted:
		return "周已执行不可交换"
	case ReasonPhaseMismatch:
		return "航季阶段不符"
	case ReasonRegisterLate:
		return "登记超期"
	case ReasonDuplicateRegister:
		return "重复登记"
	case ReasonCapacity:
		return "容量不足"
	}
	return "未知"
}

// RejectError 是被拒绝操作返回的错误，携带最靠前的一类拒绝原因。
type RejectError struct {
	Reason Reason
	Detail string
}

func (e *RejectError) Error() string { return fmt.Sprintf("%s: %s", e.Reason, e.Detail) }

func reject(r Reason, format string, args ...any) *RejectError {
	return &RejectError{Reason: r, Detail: fmt.Sprintf(format, args...)}
}

// Config 系统配置。时间均为抽象逻辑时钟（int64），一周固定为 WeekLength 个时间单位。
type Config struct {
	TotalWeeks               int   // 航季总周数
	MinSeriesWeeks           int   // 系列最少周数
	CapacityPerSlot          int   // 每个（星期几, 小时段）单元格每周容量上限
	HistoricThresholdPercent int   // 历史优先权达标比例（百分数，恰等于视为达标）
	NewcomerThreshold        int   // 新进入者阈值：持有系列数少于此值即为新进入者
	RequestDeadline          int64 // 申请截止时刻（须在此之前提交）
	ReturnDeadline           int64 // 返还截止时刻（此前返还的周不计入分母）
	RegistrationWindow       int64 // 周结束后的登记窗口长度（窗口终点取闭）
	SeasonStart              int64 // 第 0 周开始时刻
	WeekLength               int64 // 一周的时间单位数
}

func (c Config) weekEnd(week int) int64 { return c.SeasonStart + int64(week+1)*c.WeekLength }
func (c Config) seasonEnd() int64       { return c.weekEnd(c.TotalWeeks - 1) }

// SlotKey 标识一个（星期几, 小时段）；CellKey 标识一个单元格（某周某天某小时段）。
type SlotKey struct{ Weekday, Hour int }
type CellKey struct{ Week, Weekday, Hour int }

// HistoricKey 历史资格：公司 + 星期几 + 小时段 + 同等周数范围。
type HistoricKey struct {
	Airline            string
	Weekday, Hour      int
	StartWeek, EndWeek int
}

// WeekState 系列中某一周的状态；零值 WeekPlanned 表示已计划但未登记。
type WeekState uint8

const (
	WeekPlanned       WeekState = iota // 已计划，未登记
	WeekExecuted                       // 已执行
	WeekUnexecuted                     // 已登记未执行（含结算时按未执行处理）
	WeekReturnedEarly                  // 返还截止前返还（不计入分母）
	WeekReturnedLate                   // 返还截止后返还（计入分母不计入分子）
	WeekExempted                       // 不可抗力豁免（不计入分子分母）
)

// Series 一个起降时刻系列。
type Series struct {
	ID                 int
	Airline            string // 当前持有者（交换会改变）
	Weekday, Hour      int
	StartWeek, EndWeek int
	Weeks              map[int]WeekState // 仅记录非 WeekPlanned 的周
}

func (s *Series) weekState(w int) WeekState {
	if st, ok := s.Weeks[w]; ok {
		return st
	}
	return WeekPlanned
}

// Request 一次航季申请。
type Request struct {
	ID                 int
	Airline            string
	Weekday, Hour      int
	StartWeek, EndWeek int
	SubmittedAt        int64
	Historic           bool // 是否享有历史优先权
}

// RequestSpec 申请/直接分配的参数。
type RequestSpec struct {
	Airline            string
	Weekday, Hour      int
	StartWeek, EndWeek int
}

func (s RequestSpec) historicKey() HistoricKey {
	return HistoricKey{Airline: s.Airline, Weekday: s.Weekday, Hour: s.Hour, StartWeek: s.StartWeek, EndWeek: s.EndWeek}
}

// Stats 可验证的性能计数器，用于证明关键操作开销不随机场规模增长。
type Stats struct {
	CellReads      int // 单元格占用查询次数
	TreeNodeVisits int // 线段树节点访问次数
	WeekReads      int // 周状态读取次数（使用率计算）
}
