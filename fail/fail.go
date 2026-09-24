// Package fail 维护任务终态机、跳过传播与取消标记。
package fail

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/graph"
)

// 终态枚举，四类互相可区分。
const (
	StatusPending  = "pending"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusSkipped  = "skipped"
	StatusCanceled = "canceled"
)

var (
	ErrTaskFailed = errors.New("fail: task returned error")
	ErrTaskPanic  = errors.New("fail: task panicked")
)

// PanicError 包装 recover 到的原始 panic 值。
type PanicError struct{ Value any }

func (e *PanicError) Error() string {
	return fmt.Sprintf("%v", e.Value)
}
func (e *PanicError) Unwrap() error { return ErrTaskPanic }

// WrapPanic 把 panic 值转成可 errors.Is(err, ErrTaskPanic) 的失败错误。
func WrapPanic(v any) error {
	return fmt.Errorf("%w: %w", ErrTaskPanic, &PanicError{Value: v})
}

// State 是单个任务的最终状态记录。
type State struct {
	ID       string
	Status   string
	Err      error  // failed 时的原始错误
	ReasonID string // skipped 时的根失败任务
	Started  bool   // 是否真正开始执行过
}

// Tracker 是并发安全的状态存储。
type Tracker struct {
	mu     sync.Mutex
	g      *graph.Graph
	states map[string]*State
}

func NewTracker(g *graph.Graph) *Tracker {
	t := &Tracker{g: g, states: map[string]*State{}}
	for _, id := range g.Nodes() {
		t.states[id] = &State{ID: id, Status: StatusPending}
	}
	return t
}

// MarkStarted 记录任务真正开始执行（用于区分 canceled 与 skipped）。
func (t *Tracker) MarkStarted(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st := t.states[id]; st != nil {
		st.Started = true
	}
}

// Complete 写入终态；若任务已处于终态（如先被标 canceled），结果被丢弃。
func (t *Tracker) Complete(id, status string, err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.states[id]
	if st == nil || st.Status != StatusPending {
		return false
	}
	st.Status = status
	st.Err = err
	return true
}

// MarkSkipped 把尚未结束且未启动的节点标为 skipped。返回是否发生了新标记。
func (t *Tracker) MarkSkipped(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.states[id]
	if st == nil || st.Status != StatusPending || st.Started {
		return false
	}
	st.Status = StatusSkipped
	return true
}

// CancelOne 把单个 pending 节点标为 canceled（调度器中止路径专用）。
func (t *Tracker) CancelOne(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.states[id]
	if st == nil || st.Status != StatusPending {
		return false
	}
	st.Status = StatusCanceled
	return true
}

// CancelPending 把所有满足 pred 的 pending 节点标为 canceled。
// startedOnly=true 时只取已启动旁支（首失败瞬间预标，迟到结果随之丢弃）。
func (t *Tracker) CancelPending(startedOnly bool) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ids []string
	for _, id := range t.g.Nodes() {
		st := t.states[id]
		if st != nil && st.Status == StatusPending &&
			(!startedOnly || st.Started) {
			st.Status = StatusCanceled
			ids = append(ids, id)
		}
	}
	return ids
}

// AllDone 报告是否所有节点都已进入终态。
func (t *Tracker) AllDone() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, st := range t.states {
		if st.Status == StatusPending {
			return false
		}
	}
	return true
}

func (t *Tracker) Snapshot() []State {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]State, 0, len(t.states))
	for _, id := range t.g.Nodes() {
		out = append(out, *t.states[id])
	}
	t.fillReasonsLocked(out)
	return out
}

func (t *Tracker) StateOf(id string) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.states[id]
}

// fillReasonsLocked 为每个 skipped 节点静态计算根原因：
// 逆边 DFS 所有失败祖先，取 ID 字典序最小者，与完成时序无关。
func (t *Tracker) fillReasonsLocked(out []State) {
	for i := range out {
		if out[i].Status != StatusSkipped {
			continue
		}
		root := t.minFailedAncestor(out[i].ID)
		out[i].ReasonID = root
	}
}

func (t *Tracker) minFailedAncestor(id string) string {
	seen := map[string]bool{id: true}
	stack := append([]string{}, t.g.Preds(id)...)
	var failed []string
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[u] {
			continue
		}
		seen[u] = true
		if st := t.states[u]; st != nil && st.Status == StatusFailed {
			failed = append(failed, u)
		}
		stack = append(stack, t.g.Preds(u)...)
	}
	sort.Strings(failed)
	if len(failed) == 0 {
		return ""
	}
	return failed[0]
}
