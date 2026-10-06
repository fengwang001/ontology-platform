// Package occupancy 实现占道施工许可的时段冲突审查与应急抢修抢占服务。
//
// 能力
//   - 路段车道封闭申请受理（常规 / 应急），左闭右开时段。
//   - 同路段车道数冲突、指定绕行路线双向冲突、走廊级并发封闭上限。
//   - 应急抢修抢占常规许可：未开始整体顺延、已开始截断后剩余顺延，按原批准
//     序依次重审，失败转待重排。
//   - 已批许可延期（延长按新申请规则审查新增部分、缩短免审）与撤销。
//   - 任意路段任意时刻的封闭车道数与生效许可查询，开销不随历史许可总数增长。
//   - 单调操作时钟、可区分且按优先级只报一类的错误码、完整状态历史与判定日志。
//
// 典型用法
//
//	s, _ := occupancy.NewService(cfg)
//	r := s.Apply(occupancy.ApplyRequest{OpTime: 0, Road: "a", Lanes: 1,
//	    Start: 10, End: 20, Priority: occupancy.Regular})
//	if r.Reason.None() { /* r.PermitID */ }
//	q, _ := s.Query(occupancy.QueryRequest{Road: "a", At: 15})
//
// 设计取舍见 DESIGN.md；朴素全量重判模型见 NaiveModel，仅用于差分测试。

package occupancy

import "fmt"

// Priority 是许可优先级：常规或应急。
type Priority int

const (
	// Regular 常规许可，受同路段、绕行、走廊上限三类规则约束。
	Regular Priority = iota
	// Emergency 应急抢修许可，仅受参数与同路段车道数约束。
	Emergency
)

func (p Priority) String() string {
	switch p {
	case Regular:
		return "regular"
	case Emergency:
		return "emergency"
	default:
		return fmt.Sprintf("priority(%d)", int(p))
	}
}

// Status 是许可在任一时刻所处的状态。
type Status int

const (
	// Approved 已批（含被抢占后顺延审查通过、延期成功的许可）。
	Approved Status = iota
	// PendingReschedule 待重排：被应急抢占后顺延审查未通过。
	PendingReschedule
	// Revoked 已撤销，不再参与任何判定。
	Revoked
)

func (s Status) String() string {
	switch s {
	case Approved:
		return "approved"
	case PendingReschedule:
		return "pending_reschedule"
	case Revoked:
		return "revoked"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// Interval 是左闭右开的整数时刻区间 [Start, End)。
type Interval struct {
	Start int64
	End   int64
}

// overlaps 判断两个左闭右开区间是否相交；首尾相接不算相交。
func (i Interval) overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}

// contains 判断时刻 t 是否落在区间内。
func (i Interval) contains(t int64) bool {
	return i.Start <= t && t < i.End
}

// Road 描述路网中的一条路段。
type Road struct {
	ID       string
	Lanes    int
	Corridor string
	// Detour 是该路段被全封闭时指定的绕行路线，元素为其他路段 ID。
	Detour []string
}

// Config 是路网与走廊并发上限的静态配置。
type Config struct {
	Roads       []Road
	CorridorCap map[string]int
}

// Permit 是一份占道施工许可的可复现快照。
type Permit struct {
	ID       int64
	Road     string
	Lanes    int
	Interval Interval
	Priority Priority
	Status   Status
	// ApprovedSeq 是原批准操作的全局序号，被抢占许可顺延时按它排序。
	ApprovedSeq int64
	// curKey 是该许可当前占用片段在路段活跃索引中的键；0 表示当前不占位。
	curKey int64
}

// ApplyRequest 是受理申请（常规或应急）。
type ApplyRequest struct {
	OpTime   int64
	Road     string
	Lanes    int
	Start    int64
	End      int64
	Priority Priority
}

// ExtendRequest 是已批许可的截止时刻变更；NewEnd 小于原截止即缩短。
type ExtendRequest struct {
	OpTime   int64
	PermitID int64
	NewEnd   int64
}

// RevokeRequest 撤销一份未结束（含待重排、被抢占处理中）的许可。
type RevokeRequest struct {
	OpTime   int64
	PermitID int64
}

// QueryRequest 查询某路段在某时刻的封闭车道数与生效许可。
type QueryRequest struct {
	Road string
	At   int64
}

// QueryResult 是查询结果。Active 为该时刻生效（已批或应急）的许可快照。
type QueryResult struct {
	ClosedLanes int
	Active      []Permit
}

// ApplyResult 是受理结果。被拒绝时 Reason 非空且 PermitID 为 0。
type ApplyResult struct {
	PermitID int64
	Permit   Permit
	Reason   CodeError
	// Preempted 记录本次受理触发抢占的许可 ID 及其顺延结果，供判定日志使用。
	Preempted []PreemptRecord
}

// PreemptRecord 描述一份被应急许可抢占的常规许可的处理结果。
type PreemptRecord struct {
	PermitID  int64
	Truncated bool
	NewStart  int64
	NewEnd    int64
	Approved  bool
	Reason    CodeError
}

// MutationResult 是延期、撤销的结果。
type MutationResult struct {
	Permit Permit
	Reason CodeError
}

// EventKind 标识一次操作的种类。
type EventKind string

const (
	EventApply  EventKind = "apply"
	EventExtend EventKind = "extend"
	EventRevoke EventKind = "revoke"
)

// Event 是操作日志中的一条记录；拒绝的操作也会以 Accepted=false 留痕。
type Event struct {
	Seq       int64
	OpTime    int64
	Kind      EventKind
	Road      string
	PermitID  int64
	Lanes     int
	Start     int64
	End       int64
	Priority  Priority
	NewEnd    int64
	Accepted  bool
	Reason    CodeError
	Permit    Permit
	Preempted []PreemptRecord
}

// StatusChange 是许可状态历史中的一次变迁。
type StatusChange struct {
	Seq      int64
	OpTime   int64
	PermitID int64
	Before   Status
	After    Status
	Interval Interval
	Note     string
}
