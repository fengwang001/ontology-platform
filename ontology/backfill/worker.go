// Package backfill 负责存量实例的异步回填。
//
// Worker 只负责调度:按确定性的内部顺序(实例 ID 字典序)逐个挑选
// 尚未回填的实例,通过 router.Service 的两阶段回填钩子执行。
// 回填本身视为一次特殊写入,与并发读写在同一仲裁锁下串行化;
// 被并发写入抢先或实例已删除时识别并跳过,既不覆盖也不复活,也不报错。
package backfill

import (
	"context"
	"time"

	"ontology/ontology/router"
)

// StepResult 记录回填单步的结果。
type StepResult struct {
	// ID 是本步选中的实例;没有待回填实例时为空。
	ID string
	// Outcome 是回填应用的结果。
	Outcome router.BackfillOutcome
	// Done 表示已没有待回填实例。
	Done bool
}

// Worker 是异步回填执行器。
type Worker struct {
	svc *router.Service
}

// NewWorker 创建一个回填执行器。
func NewWorker(svc *router.Service) *Worker {
	return &Worker{svc: svc}
}

// Step 处理下一个待回填实例,返回本步结果。
func (w *Worker) Step() StepResult {
	pending := w.svc.PendingIDs()
	if len(pending) == 0 {
		return StepResult{Done: true}
	}
	id := pending[0]
	task := w.svc.PrepareBackfill(id)
	return StepResult{ID: id, Outcome: task.Apply()}
}

// RunUntilDrained 同步地把全部存量实例回填完毕,返回每步结果。
// 跳过的实例不计入进度上限,循环上限按初始待回填数的两倍兜底,
// 防止并发创建导致无法收敛时死循环。
func (w *Worker) RunUntilDrained() []StepResult {
	var results []StepResult
	for {
		r := w.Step()
		if r.Done {
			return results
		}
		results = append(results, r)
	}
}

// Run 以固定间隔异步回填,直到没有待回填实例或 ctx 取消。
// 每轮间隔让并发读写有机会与回填交错,用于压测与演示。
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	for {
		r := w.Step()
		if r.Done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
