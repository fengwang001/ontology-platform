// Package slots 实现机场起降时刻协调与历史优先权系统。
//
// 航空公司按航季申请成系列的起降时刻；上一航季使用率达标（不低于配置
// 比例，恰等于视为达标）的系列，其承运公司在本航季对同星期几、同小时段、
// 同周数范围的申请享有历史优先权。申请截止时一次性全有或全无分配，之后
// 航季内可返还、交换、登记执行，航季结束结算并产生下一航季历史资格清单。
package slots

import (
	"errors"
	"time"
)

// 拒绝类别，按统一拒绝次序排列：
// 参数非法 > 时钟回退 > 公司或系列不存在 > 航季阶段不符 > 登记超期 > 重复登记 > 容量不足。
var (
	ErrInvalidParam      = errors.New("参数非法")
	ErrClockRegression   = errors.New("时钟回退")
	ErrNotFound          = errors.New("公司或系列不存在")
	ErrRequestsClosed    = errors.New("航季阶段不符：申请已截止")
	ErrRequestsOpen      = errors.New("航季阶段不符：申请未截止")
	ErrNotAllocated      = errors.New("航季阶段不符：尚未执行分配")
	ErrAlreadyAllocated  = errors.New("航季阶段不符：分配已完成")
	ErrSeasonNotEnded    = errors.New("航季阶段不符：航季未结束")
	ErrSeasonSettled     = errors.New("航季阶段不符：航季已结算")
	ErrWeekExecuted      = errors.New("航季阶段不符：周已执行不可交换")
	ErrWeekNotEnded      = errors.New("航季阶段不符：周未结束不可登记")
	ErrRegistrationLate  = errors.New("登记超期")
	ErrDuplicateRegister = errors.New("重复登记")
	ErrCapacity          = errors.New("容量不足")
)

// Rank 返回拒绝类别在统一拒绝次序中的序号，序号小者优先报告；未知错误返回 -1。
func Rank(err error) int {
	switch err {
	case ErrInvalidParam:
		return 0
	case ErrClockRegression:
		return 1
	case ErrNotFound:
		return 2
	case ErrRequestsClosed, ErrRequestsOpen, ErrNotAllocated, ErrAlreadyAllocated,
		ErrSeasonNotEnded, ErrSeasonSettled, ErrWeekExecuted, ErrWeekNotEnded:
		return 3
	case ErrRegistrationLate:
		return 4
	case ErrDuplicateRegister:
		return 5
	case ErrCapacity:
		return 6
	}
	return -1
}

// IsPhase 报告 err 是否属于“航季阶段不符”类别。
func IsPhase(err error) bool { return Rank(err) == 3 }

// WeekStatus 是系列中某一周的登记状态。
type WeekStatus int

const (
	Unregistered WeekStatus = iota // 未登记
	Executed                       // 已执行
	NotExecuted                    // 未执行
	Exempt                         // 豁免（不可抗力，不计入分子分母）
)

// Config 是系统配置。
type Config struct {
	SeasonStart           time.Time     // 航季起始时刻（第 1 周开始）
	TotalWeeks            int           // 航季总周数
	MinWeeks              int           // 系列最少周数
	Capacity              int           // 每（星期几, 小时段）单元格每周容量上限
	UsageThresholdPercent int           // 历史优先权达标比例（百分数，恰等于视为达标）
	NewEntrantThreshold   int           // 新进入者阈值：持有系列数少于此值即为新进入者
	RequestDeadline       time.Time     // 申请截止时刻（须在此之前提交）
	ReturnDeadline        time.Time     // 返还截止时刻（此前返还的周不计入分母）
	RegisterWindow        time.Duration // 周结束后的登记窗口（终点取闭）
}

// Request 是一条航季申请。
type Request struct {
	ID        int
	Carrier   string
	Weekday   int // 星期几，0..6
	Hour      int // 小时段，0..23
	StartWeek int // 起始周，1 起
	EndWeek   int // 结束周，闭区间
	SubmitAt  time.Time
	Historic  bool // 是否享有历史优先权
	Allocated bool // 是否已转为系列
}

// Series 是一个已分配的时刻系列。
type Series struct {
	ID        int
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
	Holder    string // 当前持有者（交换后变更）
	// returned 与 status 均按周偏移（week-StartWeek）索引。
	// returned: 0 未返还，1 截止前返还，2 截止后返还。
	returned []uint8
	status   []WeekStatus
}

// Weeks 返回系列周数。
func (s *Series) Weeks() int { return s.EndWeek - s.StartWeek + 1 }

// Returned 报告某周是否已返还，以及是否在返还截止时刻前返还。
func (s *Series) Returned(week int) (returned, beforeDeadline bool) {
	mark := s.returned[week-s.StartWeek]
	return mark != 0, mark == 1
}

// Status 返回某周的登记状态。
func (s *Series) Status(week int) WeekStatus { return s.status[week-s.StartWeek] }

// Eligibility 是一条下一航季历史资格。
type Eligibility struct {
	Carrier   string
	Weekday   int
	Hour      int
	StartWeek int
	EndWeek   int
}

// RequestOutcome 是分配结算中一条申请的结果。
type RequestOutcome struct {
	RequestID int
	Carrier   string
	Historic  bool
	Allocated bool
	Reason    error // 未被满足时为 ErrCapacity
}

// AllocationResult 是申请截止时一次性结算的结果。
type AllocationResult struct {
	Outcomes []RequestOutcome
}

// AllocatedIDs 返回所有被满足的申请 ID（按提交次序）。
func (r *AllocationResult) AllocatedIDs() []int {
	var ids []int
	for _, o := range r.Outcomes {
		if o.Allocated {
			ids = append(ids, o.RequestID)
		}
	}
	return ids
}
