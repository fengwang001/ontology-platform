// Package slots 实现单机场单航季的起降时刻协调与历史优先权系统。
//
// 一个航季由连续若干周组成，每周 7 天、每天 24 个小时段；时刻系列覆盖
// [StartWeek,EndWeek] 闭区间内每周同一天同一小时段的一次起降。申请在截止前
// 提交，截止时一次性按“历史优先权 → 新进入者保留额 → 其余申请”三段结算，
// 未满足者进入该小时段按提交时刻排序的等候名单。航季内可返还单周（触发
// 名单补位）、在双方系列均未执行过时交换同周数范围系列、在周结束后的登记
// 窗口（终点取闭）内登记执行/未执行/豁免；航季结束时结算使用率并产出下一
// 航季历史资格。所有变更并发安全且对同一输入序列可复现。拒绝统一按
// ErrorOrder 只报最靠前的一类；reference.go 提供独立朴素模型供差分对照。
package slots

import (
	"fmt"
	"time"
)

// SlotError 是本系统统一返回的拒绝类别。拒绝只报最靠前的一类，
// 次序为：参数非法 > 时钟回退 > 公司或系列不存在 > 航季阶段不符
// > 登记超期 > 重复登记 > 容量不足。
type SlotError string

func (e SlotError) Error() string { return string(e) }

const (
	ErrInvalid       SlotError = "参数非法"
	ErrClock         SlotError = "时钟回退"
	ErrNotFound      SlotError = "公司或系列不存在"
	ErrApplyClosed   SlotError = "申请已截止"
	ErrSeasonSettled SlotError = "航季已结算"
	ErrWeekExecuted  SlotError = "周已执行不可交换"
	ErrRegisterLate  SlotError = "登记超期"
	ErrDupRegister   SlotError = "重复登记"
	ErrCapacity      SlotError = "容量不足"
)

// ErrorOrder 为规定的拒绝次序，可用于测试每一对相邻类别。
var ErrorOrder = []SlotError{
	ErrInvalid,
	ErrClock,
	ErrNotFound,
	ErrApplyClosed,
	ErrSeasonSettled,
	ErrWeekExecuted,
	ErrRegisterLate,
	ErrDupRegister,
	ErrCapacity,
}

// RegStatus 是某周的执行登记状态。
type RegStatus int

const (
	regUnknown     RegStatus = iota // 未登记（内部零值，外部不可传入）
	RegExecuted                     // 已执行
	RegNotExecuted                  // 未执行
	RegExempt                       // 不可抗力豁免：分子分母均不计
)

type phase int

const (
	phaseAccepting phase = iota // 申请受理中
	phaseAllocated              // 已一次性分配，航季运行中
	phaseSettled                // 航季已结算
)

// Config 为一个航季的系统配置。
type Config struct {
	Weeks               int           // 航季总周数
	WeekLength          time.Duration // 每周长度
	SeasonStart         time.Time     // 第 1 周开始时刻
	MinWeeks            int           // 系列最少周数
	Capacity            [7][24]int    // 容量上限，各周相同
	ApplyDeadline       time.Time     // 申请截止时刻（取闭语义由调用时刻判定）
	ReturnDeadline      time.Time     // 返还截止时刻（取闭）
	QualifyPercent      int           // 历史达标比例百分数，等于视为达标
	NewEntrantThreshold int           // 持有系列数 < 阈值即为新进入者
	RegisterWindow      time.Duration // 每周登记窗口长度，终点取闭
	SeasonEnd           time.Time     // 航季结束时刻
}

// Application 是一条航季申请。
type Application struct {
	ID          string
	Airline     string
	Day         int
	Hour        int
	StartWeek   int
	EndWeek     int
	SubmittedAt time.Time
}

// Series 是被满足后形成的时刻系列。
type Series struct {
	ID        string
	Airline   string
	Day       int
	Hour      int
	StartWeek int
	EndWeek   int

	returned   map[int]bool
	returnedAt map[int]time.Time
	register   map[int]RegStatus
}

// HistKey 是历史优先权资格的键：同一星期几、同一小时段、同等周数范围。
type HistKey struct {
	Day       int
	Hour      int
	StartWeek int
	EndWeek   int
}

// Qualification 是航季结算后一个系列产生的下航季历史资格记录。
type Qualification struct {
	SeriesID  string
	Airline   string
	Day       int
	Hour      int
	StartWeek int
	EndWeek   int
	Used      int
	Planned   int
	Qualified bool
}

// Snapshot 是可复现比对用的只读状态快照。
type Snapshot struct {
	Phase          string
	Series         []SeriesSnap
	Waitlist       []WaitlistSnap
	Qualifications []Qualification
}

// SeriesSnap 是快照中的系列视图。
type SeriesSnap struct {
	ID        string
	Airline   string
	Day       int
	Hour      int
	StartWeek int
	EndWeek   int
	Returned  []int
	Register  map[int]RegStatus
}

// WaitlistSnap 是某小时段的等候名单。
type WaitlistSnap struct {
	Day            int
	Hour           int
	ApplicationIDs []string
}

type cellKey struct {
	week int
	day  int
	hour int
}

type slotKey struct {
	day  int
	hour int
}

type waitEntry struct {
	app *Application
}

func (c Config) validate() error {
	if c.Weeks <= 0 || c.MinWeeks <= 0 || c.MinWeeks > c.Weeks {
		return ErrInvalid
	}
	if c.WeekLength <= 0 {
		return ErrInvalid
	}
	if c.QualifyPercent < 0 || c.QualifyPercent > 100 {
		return ErrInvalid
	}
	if c.NewEntrantThreshold < 0 || c.RegisterWindow < 0 {
		return ErrInvalid
	}
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if c.Capacity[d][h] <= 0 {
				return ErrInvalid
			}
		}
	}
	if c.SeasonStart.IsZero() || c.ApplyDeadline.IsZero() || c.SeasonEnd.IsZero() {
		return ErrInvalid
	}
	if c.ApplyDeadline.After(c.SeasonEnd) || c.ReturnDeadline.After(c.SeasonEnd) {
		return ErrInvalid
	}
	return nil
}

func (c Config) weekStart(w int) time.Time {
	return c.SeasonStart.Add(time.Duration(w-1) * c.WeekLength)
}

func (c Config) weekEnd(w int) time.Time {
	return c.SeasonStart.Add(time.Duration(w) * c.WeekLength)
}

func (c Config) validRange(day, hour, sw, ew int) bool {
	if day < 0 || day > 6 || hour < 0 || hour > 23 {
		return false
	}
	if sw < 1 || ew < sw || ew > c.Weeks {
		return false
	}
	if ew-sw+1 < c.MinWeeks {
		return false
	}
	return true
}

func histKeyFor(a *Application) HistKey {
	return HistKey{Day: a.Day, Hour: a.Hour, StartWeek: a.StartWeek, EndWeek: a.EndWeek}
}

func tracef(w interface{ Write([]byte) (int, error) }, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}
