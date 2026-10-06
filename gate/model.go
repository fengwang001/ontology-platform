// Package gatealloc 实现机场登机口分配与冲突裁决系统。
//
// 模块划分：
//   - model.go：领域类型、拒绝原因、优先级与占用区间等纯规则；
//   - tree.go：按登机口维护的增广 treap 区间树（占用索引）；
//   - system.go：加锁串行化的正式实现（指派、延误、挤占、时钟）；
//   - naive.go：独立的朴素对照实现（线性扫描，仅供测试比对）。
package gatealloc

import "fmt"

// Class 为机型/登机口等级，1 最低、3 最高。
type Class int

const (
	Class1 Class = 1
	Class2 Class = 2
	Class3 Class = 3 // 最高等级，触发相邻限制
)

// Kind 为航班属性。
type Kind int

const (
	KindIntl     Kind = 1 // 国际
	KindDomestic Kind = 2 // 国内
)

// GateKind 为登机口属性。
type GateKind int

const (
	GateIntl     GateKind = 1
	GateDomestic GateKind = 2
	GateDual     GateKind = 3 // 两用
)

// Segment 标识航班占用的段。
type Segment int

const (
	SegWhole    Segment = 0 // 整段占用
	SegDeplane  Segment = 1 // 长停留：到达后卸客段
	SegBoarding Segment = 2 // 长停留：起飞前登机段
)

func (s Segment) String() string {
	switch s {
	case SegWhole:
		return "whole"
	case SegDeplane:
		return "deplane"
	case SegBoarding:
		return "boarding"
	default:
		return fmt.Sprintf("seg%d", int(s))
	}
}

// Config 为系统级配置（单位：分钟）。
type Config struct {
	Buffer      int // 起飞后缓冲：占用区间右端 = 起飞时刻 + Buffer
	MaxStay     int // 停留上限；占用长度 > MaxStay 时拆两段，恰等于按整段
	DeplaneDur  int // 拆段后卸客段固定长度
	BoardingDur int // 拆段后登机段在起飞前的固定长度（右端另加 Buffer）
}

// GateSpec 描述一个登机口。
type GateSpec struct {
	ID       string
	MaxClass Class
	Kind     GateKind
}

// FlightSpec 描述一个航班。
type FlightSpec struct {
	ID        string
	Class     Class
	Kind      Kind
	SchedArr  int // 计划到达
	SchedDep  int // 计划起飞
	BoardLead int // 登机开始提前量
}

// Adj 声明一对相邻登机口（对称）。
type Adj struct{ A, B string }

// Reason 为统一拒绝类别，先后次序即裁决优先级。
type Reason int

const (
	RejInvalidParam  Reason = iota // 参数非法
	RejClockBack                   // 时钟回退
	RejNotFound                    // 航班或登机口不存在
	RejArrivedLocked               // 已到达或已开始登机，不可改
	RejClassIncompat               // 机型不兼容
	RejKindMismatch                // 属性不符
	RejAdjacency                   // 相邻限制
	RejTimeConflict                // 时间冲突
	RejCannotEvict                 // 双方均不可挤占
)

func (r Reason) String() string {
	switch r {
	case RejInvalidParam:
		return "invalid_param"
	case RejClockBack:
		return "clock_back"
	case RejNotFound:
		return "not_found"
	case RejArrivedLocked:
		return "arrived_or_boarding_locked"
	case RejClassIncompat:
		return "class_incompatible"
	case RejKindMismatch:
		return "kind_mismatch"
	case RejAdjacency:
		return "adjacency_violation"
	case RejTimeConflict:
		return "time_conflict"
	case RejCannotEvict:
		return "cannot_evict"
	default:
		return fmt.Sprintf("reason_%d", int(r))
	}
}

// OpError 携带拒绝类别与对拍所需的细节。
type OpError struct {
	Reason     Reason
	Message    string
	ConflictID string // 时间冲突报出的航班标识
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.ConflictID != "" {
		return fmt.Sprintf("%s: %s (conflict=%s)", e.Reason, e.Message, e.ConflictID)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Message)
}

// Eviction 记录一次挤占裁决的结果。
type Eviction struct {
	Victim string // 失去登机口的航班
	Seg    Segment
	Gate   string // 被让出的登机口
	Winner string // 保留/获得登机口的一方
	Cause  Reason // RejTimeConflict 或 RejAdjacency
}

// AssignResult 为指派结果。
type AssignResult struct {
	OK  bool
	Err *OpError
}

// DelayResult 为延误更新结果。
type DelayResult struct {
	OK          bool
	Err         *OpError
	BecameSplit bool       // 本次推移是否触发整段拆两段
	Evicted     []Eviction // 确定性顺序的挤占记录（无挤占时为空切片）
}

// DelayMask 指定延误更新改哪些时刻。
type DelayMask uint8

const (
	MaskArr DelayMask = 1 << iota
	MaskDep
)

// Occupancy 是一个具体的占用区间（左闭右开）。
type Occupancy struct {
	Flight string
	Seg    Segment
	Gate   string
	Start  int
	End    int
}

// overlap 报告两个左闭右开区间是否相交；端点相接不算相交。
func overlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart < bEnd && bStart < aEnd
}

// classOK 判定机型等级兼容性。
func classOK(flight Class, gateMax Class) bool { return flight <= gateMax }

// kindOK 判定国际/国内属性兼容性。
func kindOK(flight Kind, gate GateKind) bool {
	switch gate {
	case GateDual:
		return true
	case GateIntl:
		return flight == KindIntl
	case GateDomestic:
		return flight == KindDomestic
	default:
		return false
	}
}

// higherPriority 报告 a 的挤占优先级是否高于 b。
// 依次比较：国际先于国内；机型等级高者先；计划到达早者先；标识小者先。
func higherPriority(aKind Kind, aClass Class, aArr int, aID string,
	bKind Kind, bClass Class, bArr int, bID string) bool {
	if aKind != bKind {
		return aKind == KindIntl
	}
	if aClass != bClass {
		return aClass > bClass
	}
	if aArr != bArr {
		return aArr < bArr
	}
	return aID < bID
}

// Assignment 是快照中的单条指派。
type Assignment struct {
	Flight string
	Seg    Segment
	Gate   string
	Start  int
	End    int
}

// FlightState 是快照中的航班状态。
type FlightState struct {
	Arr, Dep     int
	Split        bool
	DeplaneGate  string // 整段占用时登机口在此字段
	BoardingGate string
}

// Snapshot 为可对拍的完整状态。
type Snapshot struct {
	Now         int
	Flights     map[string]FlightState
	Assignments []Assignment // 按 (Flight, Seg) 排序
}
