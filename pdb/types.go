// Package pdb implements a PodDisruptionBudget eviction adjudication service
// for node-drain scenarios.
package pdb

import "time"

// Phase is the pod lifecycle phase.
type Phase int

const (
	PhaseRunning   Phase = iota // 运行中
	PhasePending                // 待定
	PhaseSucceeded              // 已成功
	PhaseFailed                 // 已失败
)

// InStats reports whether pods in the phase belong to budget statistics and may
// be evicted. Succeeded/Failed pods are out of scope.
func (p Phase) InStats() bool { return p == PhaseRunning || p == PhasePending }

// Value is either an absolute non-negative integer count or an integer
// percentage in [0,100].
type Value struct {
	IsPercent bool
	Amount    int
}

// Selector requires every listed key/value pair to be equal. A nil or empty
// selector matches no pod.
type Selector map[string]string

// Pod is the externally visible pod description.
type Pod struct {
	UID       string
	Namespace string
	Labels    map[string]string
	Phase     Phase
	Ready     bool
}

// PodRef identifies a pod by namespace and UID.
type PodRef struct {
	Namespace string
	UID       string
}

// NamespacedName identifies a budget by namespace and name.
type NamespacedName struct {
	Namespace string
	Name      string
}

// BudgetSpec describes one disruption budget. Exactly one of MinAvailable and
// MaxUnavailable must be non-nil.
type BudgetSpec struct {
	Name           string
	Namespace      string
	Selector       Selector
	MinAvailable   *Value
	MaxUnavailable *Value
}

// BudgetStatus is the result of a budget query, all derived from current state.
type BudgetStatus struct {
	Expected          int // 期望总数：匹配且在统计范围内的 Pod 总数
	CurrentReady      int // 当前就绪数
	RequiredReady     int // 必须保持的就绪数（不为负）
	DisruptionAllowed int // 允许的中断额度（不为负）
	MatchedPods       []PodRef
}

// eviction tracks one in-flight eviction.
type eviction struct {
	ref       PodRef
	budget    *budgetEntry // nil when matched by no budget
	wasReady  bool         // readiness at allow time, restored on expiry/cancel
	start     time.Time
	deadline  time.Time // start + grace, left-closed
	heapIndex int
}

func (e *eviction) expired(now time.Time) bool { return !now.Before(e.deadline) }
