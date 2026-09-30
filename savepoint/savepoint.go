// Package savepoint 实现保存点恢复准入器：在新作业图与保存点之间
// 按算子标识与来源映射匹配状态、判定兼容性，使恢复要么完整可用、
// 要么零副作用地被拒绝。
package savepoint

import (
	"log"
	"sort"
	"sync"
)

// StateKind 状态项种类。
type StateKind string

const (
	KindValue StateKind = "value"
	KindList  StateKind = "list"
	KindMap   StateKind = "map"
)

// ValueType 状态项值类型。
type ValueType string

const (
	TypeInt    ValueType = "int"
	TypeLong   ValueType = "long"
	TypeString ValueType = "string"
)

// DefaultMaxParallelism 新增算子的缺省最大并行度。
const DefaultMaxParallelism = 128

// StateItem 具名状态项定义。
type StateItem struct {
	Name  string
	Kind  StateKind
	Value ValueType
}

// SavepointOperator 保存点中的算子快照。
type SavepointOperator struct {
	ID             string
	MaxParallelism int
	Items          []StateItem
}

// Savepoint 保存点，登记后不可变。
type Savepoint struct {
	ID        string
	Operators []SavepointOperator
}

// JobOperator 新作业图中的算子。
type JobOperator struct {
	ID             string
	Parallelism    int
	MaxParallelism *int    // 可选，缺省继承保存点（新增算子缺省 128）
	SourceID       *string // 可选来源标识，声明后承接该保存点算子
	Items          []StateItem
}

// JobGraph 新作业图。
type JobGraph struct {
	Operators []JobOperator
}

// Action 恢复动作。
type Action string

const (
	ActionRestoreDirect Action = "restore-direct" // 直接恢复
	ActionRestoreWiden  Action = "restore-widen"  // int 变宽为 long 后恢复
	ActionStartEmpty    Action = "start-empty"    // 空状态启动
)

// OperatorPlan 单个算子的恢复计划。
type OperatorPlan struct {
	OperatorID   string
	Action       Action
	WidenedItems []string // 需要 int->long 变宽迁移的状态项，升序
}

// RestorePlan 恢复计划，相同输入得到完全相同的结果。
type RestorePlan struct {
	JobID             string
	SavepointID       string
	Operators         []OperatorPlan // 按算子标识升序
	DroppedOperators  []string       // 被丢弃的保存点算子，升序
	DroppedStateItems []string       // 被丢弃的状态项（算子.状态项），升序
}

// Reason 拒绝原因类别，声明顺序即判定次序。
type Reason string

const (
	ReasonSavepointNotFound      Reason = "savepoint-not-found"
	ReasonJobIDOccupied          Reason = "job-id-occupied"
	ReasonInvalidJobGraph        Reason = "invalid-job-graph"
	ReasonDuplicateClaim         Reason = "duplicate-claim"
	ReasonMaxParallelismMismatch Reason = "max-parallelism-mismatch"
	ReasonUnclaimedOperators     Reason = "unclaimed-operators"
	ReasonIncompatibleStateItems Reason = "incompatible-state-items"
	ReasonMissingStateItems      Reason = "missing-state-items"
)

// Rejection 拒绝结果：只报第一类原因，同类对象全部升序列出。
type Rejection struct {
	Reason  Reason
	Objects []string
}

func (r *Rejection) Error() string { return string(r.Reason) }

// Gatekeeper 保存点恢复准入器，所有方法可并发调用。
type Gatekeeper struct {
	mu         sync.Mutex
	savepoints map[string]Savepoint
	jobs       map[string]string // 作业标识 -> 引用的保存点标识
	logger     *log.Logger
}

// NewGatekeeper 创建准入器；logger 为 nil 时使用缺省日志。
func NewGatekeeper(logger *log.Logger) *Gatekeeper {
	if logger == nil {
		logger = log.Default()
	}
	return &Gatekeeper{
		savepoints: make(map[string]Savepoint),
		jobs:       make(map[string]string),
		logger:     logger,
	}
}

// RegisterSavepoint 登记保存点，登记后不可变（内部持有副本）。
func (g *Gatekeeper) RegisterSavepoint(sp Savepoint) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.savepoints[sp.ID] = cloneSavepoint(sp)
	g.logger.Printf("register savepoint: input=%+v", sp)
}

// Restore 尝试按新作业图从保存点恢复。
// 成功返回计划并占用作业标识；失败返回拒绝原因且无任何副作用。
func (g *Gatekeeper) Restore(jobID, savepointID string, allowDiscard bool, graph JobGraph) (*RestorePlan, *Rejection) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.restoreLocked(jobID, savepointID, allowDiscard, graph)
}

// Stop 停止作业，释放作业标识及其对保存点的引用。
func (g *Gatekeeper) Stop(jobID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.jobs[jobID]; !ok {
		g.logger.Printf("stop job: input=%q result=not-found", jobID)
		return false
	}
	delete(g.jobs, jobID)
	g.logger.Printf("stop job: input=%q result=stopped", jobID)
	return true
}

// DeleteSavepoint 删除保存点；被存活作业引用时拒绝。
func (g *Gatekeeper) DeleteSavepoint(savepointID string) *Rejection {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.savepoints[savepointID]; !ok {
		rej := &Rejection{Reason: ReasonSavepointNotFound, Objects: []string{savepointID}}
		g.logger.Printf("delete savepoint: input=%q rejected=%s objects=%v", savepointID, rej.Reason, rej.Objects)
		return rej
	}
	var holders []string
	for jobID, spID := range g.jobs {
		if spID == savepointID {
			holders = append(holders, jobID)
		}
	}
	if len(holders) > 0 {
		sortStrings(holders)
		rej := &Rejection{Reason: ReasonJobIDOccupied, Objects: holders}
		g.logger.Printf("delete savepoint: input=%q rejected=%s objects=%v", savepointID, rej.Reason, rej.Objects)
		return rej
	}
	delete(g.savepoints, savepointID)
	g.logger.Printf("delete savepoint: input=%q result=deleted", savepointID)
	return nil
}

func (g *Gatekeeper) restoreLocked(jobID, savepointID string, allowDiscard bool, graph JobGraph) (*RestorePlan, *Rejection) {
	g.logger.Printf("restore: input job=%q savepoint=%q allowDiscard=%v graph=%+v", jobID, savepointID, allowDiscard, graph)

	reject := func(reason Reason, objects []string) (*RestorePlan, *Rejection) {
		sortStrings(objects)
		g.logger.Printf("restore: job=%q rejected reason=%s objects=%v", jobID, reason, objects)
		return nil, &Rejection{Reason: reason, Objects: objects}
	}

	// 1. 保存点不存在
	sp, ok := g.savepoints[savepointID]
	if !ok {
		return reject(ReasonSavepointNotFound, []string{savepointID})
	}

	// 2. 作业标识已被占用
	if _, occupied := g.jobs[jobID]; occupied {
		return reject(ReasonJobIDOccupied, []string{jobID})
	}

	spOps := make(map[string]SavepointOperator, len(sp.Operators))
	for _, op := range sp.Operators {
		spOps[op.ID] = op
	}

	// 承接关系：声明来源则承接来源算子，否则承接同名保存点算子，都没有则为新增算子。
	claimOf := make(map[string]string, len(graph.Operators)) // 新算子 -> 保存点算子
	maxOf := make(map[string]int, len(graph.Operators))      // 新算子 -> 生效最大并行度
	for _, op := range graph.Operators {
		claimed := ""
		if op.SourceID != nil {
			if _, exists := spOps[*op.SourceID]; exists {
				claimed = *op.SourceID
			}
		} else if _, exists := spOps[op.ID]; exists {
			claimed = op.ID
		}
		claimOf[op.ID] = claimed
		switch {
		case op.MaxParallelism != nil:
			maxOf[op.ID] = *op.MaxParallelism
		case claimed != "":
			maxOf[op.ID] = spOps[claimed].MaxParallelism
		default:
			maxOf[op.ID] = DefaultMaxParallelism
		}
	}

	// 3. 作业图非法：标识为空或重复、p 非正或大于最大并行度、来源不在保存点内
	var invalid []string
	seen := make(map[string]bool, len(graph.Operators))
	for _, op := range graph.Operators {
		bad := op.ID == "" || seen[op.ID] ||
			op.Parallelism <= 0 || op.Parallelism > maxOf[op.ID] ||
			(op.SourceID != nil && claimOf[op.ID] == "")
		if bad && !contains(invalid, op.ID) {
			invalid = append(invalid, op.ID)
		}
		seen[op.ID] = true
	}
	if len(invalid) > 0 {
		return reject(ReasonInvalidJobGraph, invalid)
	}

	// 4. 同一保存点算子被多个新算子承接
	claimCount := make(map[string]int)
	for _, spID := range claimOf {
		if spID != "" {
			claimCount[spID]++
		}
	}
	var dupClaims []string
	for spID, n := range claimCount {
		if n > 1 {
			dupClaims = append(dupClaims, spID)
		}
	}
	if len(dupClaims) > 0 {
		return reject(ReasonDuplicateClaim, dupClaims)
	}

	// 5. 承接者最大并行度必须等于保存点的 m
	var mismatched []string
	for _, op := range graph.Operators {
		if spID := claimOf[op.ID]; spID != "" && maxOf[op.ID] != spOps[spID].MaxParallelism {
			mismatched = append(mismatched, op.ID)
		}
	}
	if len(mismatched) > 0 {
		return reject(ReasonMaxParallelismMismatch, mismatched)
	}

	// 6. 未被承接的保存点算子，仅在允许丢弃时可恢复
	var droppedOps []string
	for _, spOp := range sp.Operators {
		if claimCount[spOp.ID] == 0 {
			droppedOps = append(droppedOps, spOp.ID)
		}
	}
	if len(droppedOps) > 0 && !allowDiscard {
		return reject(ReasonUnclaimedOperators, droppedOps)
	}

	// 7. 同名状态项兼容性：种类须相同，值类型相同或 int 变宽为 long
	var incompatible []string
	widened := make(map[string][]string) // 新算子 -> 变宽状态项
	for _, op := range graph.Operators {
		spID := claimOf[op.ID]
		if spID == "" {
			continue
		}
		newItems := make(map[string]StateItem, len(op.Items))
		for _, it := range op.Items {
			newItems[it.Name] = it
		}
		for _, old := range spOps[spID].Items {
			cur, exists := newItems[old.Name]
			if !exists {
				continue
			}
			switch {
			case cur.Kind != old.Kind:
				incompatible = append(incompatible, op.ID+"."+old.Name)
			case cur.Value == old.Value:
			case old.Value == TypeInt && cur.Value == TypeLong:
				widened[op.ID] = append(widened[op.ID], old.Name)
			default:
				incompatible = append(incompatible, op.ID+"."+old.Name)
			}
		}
	}
	if len(incompatible) > 0 {
		return reject(ReasonIncompatibleStateItems, incompatible)
	}

	// 8. 被承接算子中新图缺失的状态项，仅在允许丢弃时可恢复
	var droppedItems []string
	for _, op := range graph.Operators {
		spID := claimOf[op.ID]
		if spID == "" {
			continue
		}
		newItems := make(map[string]bool, len(op.Items))
		for _, it := range op.Items {
			newItems[it.Name] = true
		}
		for _, old := range spOps[spID].Items {
			if !newItems[old.Name] {
				droppedItems = append(droppedItems, spID+"."+old.Name)
			}
		}
	}
	if len(droppedItems) > 0 && !allowDiscard {
		return reject(ReasonMissingStateItems, droppedItems)
	}

	// 成功：生成确定性计划并占用作业标识。
	plan := &RestorePlan{JobID: jobID, SavepointID: savepointID}
	for _, op := range graph.Operators {
		opPlan := OperatorPlan{OperatorID: op.ID, Action: ActionStartEmpty}
		if claimOf[op.ID] != "" {
			if w := widened[op.ID]; len(w) > 0 {
				sortStrings(w)
				opPlan.Action = ActionRestoreWiden
				opPlan.WidenedItems = w
			} else {
				opPlan.Action = ActionRestoreDirect
			}
		}
		plan.Operators = append(plan.Operators, opPlan)
	}
	sort.Slice(plan.Operators, func(i, j int) bool {
		return plan.Operators[i].OperatorID < plan.Operators[j].OperatorID
	})
	sortStrings(droppedOps)
	sortStrings(droppedItems)
	plan.DroppedOperators = droppedOps
	plan.DroppedStateItems = droppedItems

	g.jobs[jobID] = savepointID
	g.logger.Printf("restore: job=%q accepted plan=%+v", jobID, plan)
	return plan, nil
}

func cloneSavepoint(sp Savepoint) Savepoint {
	out := Savepoint{ID: sp.ID, Operators: make([]SavepointOperator, len(sp.Operators))}
	for i, op := range sp.Operators {
		items := make([]StateItem, len(op.Items))
		copy(items, op.Items)
		out.Operators[i] = SavepointOperator{ID: op.ID, MaxParallelism: op.MaxParallelism, Items: items}
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
