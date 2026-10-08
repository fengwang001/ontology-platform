// Package heating 实现城市供热管网的阀门隔离与停供影响推演。
//
// 包内按职责划分为五个协作模块：
//   - model.go     领域类型与可区分错误
//   - network.go   管网状态：拓扑与阀门变更、供热状态维护、并发控制
//   - isolation.go 隔离域推演（只读，开销与隔离域规模相关）
//   - impact.go    停供影响计算（只读，开销与受影响区域相关）
//   - execute.go   执行隔离与修复完成（全有或全无）
package heating

import "errors"

// NodeKind 节点类别。
type NodeKind int

const (
	NodeSource NodeKind = iota // 热源
	NodeBranch                 // 分支点
	NodeUser                   // 用户入口
)

// ValveState 阀门状态。
type ValveState int

const (
	ValveOpen        ValveState = iota // 开
	ValveClosed                        // 关
	ValveStuckOpen                     // 卡死在开（仅现场上报产生）
	ValveStuckClosed                   // 卡死在关（仅现场上报产生）
)

// closed 报告该状态在连通意义上是否视为“关”。
func (s ValveState) closed() bool {
	return s == ValveClosed || s == ValveStuckClosed
}

// stuck 报告该状态是否为卡死（不接受开关指令）。
func (s ValveState) stuck() bool {
	return s == ValveStuckOpen || s == ValveStuckClosed
}

// End 管段端。
type End int

const (
	EndA End = iota
	EndB
)

func (e End) other() End {
	if e == EndA {
		return EndB
	}
	return EndA
}

// 错误按固定次序判定：参数非法 > 管段不存在 > 已/未处于隔离 > 无法隔离 > 阀门状态已变化。
var (
	ErrInvalidParam       = errors.New("heating: 参数非法")
	ErrSegmentNotFound    = errors.New("heating: 管段不存在")
	ErrSegmentIsolated    = errors.New("heating: 管段已处于活动隔离")
	ErrSegmentNotIsolated = errors.New("heating: 管段未处于隔离")
	ErrNotIsolatable      = errors.New("heating: 无法隔离")
	ErrValveStateChanged  = errors.New("heating: 阀门状态已变化")

	ErrNodeNotFound       = errors.New("heating: 节点不存在")
	ErrNodeExists         = errors.New("heating: 节点已存在")
	ErrSegmentExists      = errors.New("heating: 管段已存在")
	ErrSelfLoop           = errors.New("heating: 管段不得自环")
	ErrSegmentUnderRepair = errors.New("heating: 管段处于抢修中")
	ErrValveNotFound      = errors.New("heating: 阀门不存在")
	ErrValveExists        = errors.New("heating: 阀门已存在")
	ErrValveStuck         = errors.New("heating: 阀门卡死，不接受开关指令")
	ErrValveNotStuck      = errors.New("heating: 阀门未处于卡死状态")
)

// Plan 隔离推演结果。推演为只读，对同一状态结果唯一。
type Plan struct {
	Segment       string   // 目标管段
	ValvesToClose []string // 需要新关闭的阀门（已关/卡死在关不列入），有序
	Domain        []string // 隔离域管段集合，有序
	AffectedUsers []string // 隔离后失去供热的用户入口（计入叠加影响），有序

	// 推演过程中访问的管段/节点数，用于验证开销只与受影响区域相关。
	ExploredSegments int
	ExploredNodes    int
}
