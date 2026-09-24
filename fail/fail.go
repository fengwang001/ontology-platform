// Package fail 定义任务状态与失败模式，实现失败传播与取消标记。
package fail

import (
	"errors"
	"fmt"

	"ontology/graph"
)

// Status 是任务状态；四类终态为 Succeeded/Failed/Skipped/Canceled。
type Status int

const (
	Pending Status = iota
	Running
	Succeeded
	Failed
	Skipped
	Canceled
)

var statusNames = []string{"pending", "running", "succeeded", "failed", "skipped", "canceled"}

func (s Status) String() string {
	if int(s) >= 0 && int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "unknown"
}

// Mode 决定首个失败发生后的传播策略。
type Mode int

const (
	FailFast   Mode = iota // 首个失败即传播、取消在跑任务并停止派发
	BestEffort             // 不依赖失败任务的分支继续跑完
)

// ErrSkipped/ErrCanceled 可用 errors.Is 区分跳过与取消。
var (
	ErrSkipped  = errors.New("task skipped")
	ErrCanceled = errors.New("task canceled")
)

// Tracker 记录每个任务的状态、原因与错误，由调度器单 goroutine 驱动。
type Tracker struct {
	status map[string]Status
	cause  map[string]string
	errs   map[string]error
}

func NewTracker(ids []string) *Tracker {
	t := &Tracker{status: map[string]Status{}, cause: map[string]string{}, errs: map[string]error{}}
	for _, id := range ids {
		t.status[id] = Pending
	}
	return t
}

func (t *Tracker) Status(id string) Status { return t.status[id] }
func (t *Tracker) Cause(id string) string  { return t.cause[id] }
func (t *Tracker) Err(id string) error     { return t.errs[id] }

// Start 标记任务开始执行；只有 Start 过的任务才可能被 Cancel。
func (t *Tracker) Start(id string) { t.status[id] = Running }

func (t *Tracker) Succeed(id string) { t.status[id] = Succeeded }

func (t *Tracker) Fail(id string, err error) {
	t.status[id] = Failed
	t.errs[id] = err
}

// Cancel 把已开始执行的任务定格为被取消；其之后写回的结果一律丢弃。
func (t *Tracker) Cancel(id string) {
	t.status[id] = Canceled
	t.errs[id] = ErrCanceled
}

// PropagateSkip 把 failedID 的所有未开始下游标记为 Skipped，Cause 一律
// 指向最初失败的任务 cause（而非直接上游）。返回遍历的边数与新跳过的任务数。
func (t *Tracker) PropagateSkip(g *graph.Graph, failedID, cause string) (edges, skipped int) {
	stack := []string{failedID}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, dep := range g.Dependents(cur) {
			edges++
			if t.status[dep] != Pending {
				continue
			}
			t.status[dep] = Skipped
			t.cause[dep] = cause
			t.errs[dep] = fmt.Errorf("%w: upstream %s failed", ErrSkipped, cause)
			skipped++
			stack = append(stack, dep)
		}
	}
	return edges, skipped
}

// SkipAllPending 快速失败清扫：把仍未开始的任务标记为 Skipped；
// 已有 Cause 的（失败任务的下游）保持不变，其余指向首个失败。返回新跳过数。
func (t *Tracker) SkipAllPending(firstFailure string) (skipped int) {
	for id, s := range t.status {
		if s != Pending {
			continue
		}
		t.status[id] = Skipped
		skipped++
		if t.cause[id] == "" {
			t.cause[id] = firstFailure
			t.errs[id] = fmt.Errorf("%w: upstream %s failed", ErrSkipped, firstFailure)
		}
	}
	return skipped
}
