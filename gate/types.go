// Package gate 实现机场登机口分配与冲突裁决。
//
// 每个航班的过站占用一个登机口一段时间；登机口有机型等级、国际/国内
// 属性与相邻限制；延误会推移占用区间并可能挤占他人。所有变更操作在
// 单互斥锁下串行化，相同操作序列重放得到完全相同的结果。
package gate

import "fmt"

// Level 为机型等级，1 最低，MaxLevel 最高。
type Level int

// MaxLevel 为最高机型等级，只有它会触发相邻限制。
const MaxLevel Level = 3

// GateKind 为登机口属性。
type GateKind int

const (
	GateInternational GateKind = iota
	GateDomestic
	GateDual
)

// FlightKind 为航班属性。
type FlightKind int

const (
	FlightInternational FlightKind = iota
	FlightDomestic
)

// SegKind 标识航班占用的分段。
type SegKind int

const (
	SegWhole   SegKind = iota // 整段占用
	SegDeplane                // 卸客段（长时停留拆分后）
	SegBoard                  // 登机段（长时停留拆分后）
)

func (k SegKind) String() string {
	switch k {
	case SegWhole:
		return "whole"
	case SegDeplane:
		return "deplane"
	case SegBoard:
		return "board"
	}
	return "unknown"
}

// Reason 为拒绝原因，声明顺序即统一拒绝次序。
type Reason int

const (
	ReasonOK Reason = iota
	ReasonInvalidParam
	ReasonClockSkew
	ReasonNotFound
	ReasonImmutable
	ReasonLevelIncompatible
	ReasonKindMismatch
	ReasonAdjacency
	ReasonTimeConflict
	ReasonNotBumpable
)

func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonInvalidParam:
		return "invalid-param"
	case ReasonClockSkew:
		return "clock-skew"
	case ReasonNotFound:
		return "not-found"
	case ReasonImmutable:
		return "immutable"
	case ReasonLevelIncompatible:
		return "level-incompatible"
	case ReasonKindMismatch:
		return "kind-mismatch"
	case ReasonAdjacency:
		return "adjacency"
	case ReasonTimeConflict:
		return "time-conflict"
	case ReasonNotBumpable:
		return "not-bumpable"
	}
	return "unknown"
}

// Config 为系统配置，单位均为分钟。
type Config struct {
	Buffer    int // 起飞后缓冲时长
	StayLimit int // 停留上限（占用区间长度超过它则拆段）
	Deplane   int // 卸客时长
	Board     int // 登机时长
}

// GateSpec 描述一个登机口。
type GateSpec struct {
	ID       string
	MaxLevel Level
	Kind     GateKind
	Adjacent []string // 相邻登机口（须已存在），关系自动对称
}

// FlightSpec 描述一个航班。
type FlightSpec struct {
	ID        string
	Level     Level
	Kind      FlightKind
	SchedArr  int // 计划到达
	SchedDep  int // 计划起飞
	BoardLead int // 登机开始提前量
}

// Flight 为航班的当前状态。
type Flight struct {
	ID        string
	Level     Level
	Kind      FlightKind
	SchedArr  int
	SchedDep  int
	BoardLead int
	CurArr    int // 当前到达时刻
	CurDep    int // 当前起飞时刻
}

// Result 为每次变更操作的唯一可复现结论。
type Result struct {
	OK       bool
	Reason   Reason
	Conflict string   // 时间冲突时冲突的航班标识
	Bumped   []string // 被挤占转为待分配的航班标识（升序）
	Detail   string   // 判定依据（人类可读）
}

func okResult(format string, args ...any) Result {
	return Result{OK: true, Reason: ReasonOK, Detail: fmt.Sprintf(format, args...)}
}

func reject(reason Reason, format string, args ...any) Result {
	return Result{OK: false, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
