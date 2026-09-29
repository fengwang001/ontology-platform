package rename

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// FailureHook 在某一步真正生效前被调用；返回错误则该步视为失败，
// 执行器立即按逆序撤回此前已完成的步骤。
type FailureHook func(stepIndex int, from, to string) error

// batchRecord 保存最近一次成功批次的撤销信息。
type batchRecord struct {
	id    int64
	steps []step
}

// Executor 串行化批次提交，并支持对最近一次成功批次的整体撤销。
type Executor struct {
	ns         *Namespace
	tempPrefix string
	logger     *slog.Logger

	submitMu sync.Mutex // 保证批次彼此串行生效

	mu     sync.Mutex
	last   *batchRecord // 最近一次成功且尚未撤销的批次
	nextID int64
}

// NewExecutor 创建执行器。logger 为 nil 时使用 slog.Default()。
func NewExecutor(ns *Namespace, tempPrefix string, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{ns: ns, tempPrefix: tempPrefix, logger: logger, nextID: 1}
}

// Submit 串行提交一个批次。
//
// 校验不通过时拒绝整个批次且不改动任何名字；执行中某一步失败时，
// 已执行步骤按逆序撤回，命名空间逐项复原。成功后该批次成为可撤销的
// “最近一次成功批次”。
func (ex *Executor) Submit(ctx context.Context, renames []Rename, hook FailureHook) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ex.submitMu.Lock()
	defer ex.submitMu.Unlock()

	ex.logger.LogAttrs(ctx, slog.LevelInfo, "batch submitted",
		slog.Int("renames", len(renames)),
		slog.Any("input", renames),
	)

	// 整个批次（校验 + 全部单步 + 状态发布）在命名空间写锁内完成：
	// 并发读者只能看到批次之前或之后的完整命名空间，看不到临时名中间态。
	ex.ns.lockBatch()

	plan, err := buildPlan(ex.ns.snapshotLocked(), renames, ex.tempPrefix)
	if err != nil {
		ex.ns.unlockBatch()
		ex.logger.LogAttrs(ctx, slog.LevelWarn, "batch rejected",
			slog.Any("input", renames),
			slog.String("reason", reasonOf(err)),
			slog.String("detail", err.Error()),
		)
		return err
	}

	ex.logger.LogAttrs(ctx, slog.LevelInfo, "plan derived",
		slog.Int("steps", len(plan.steps)),
		slog.Any("temporary_names", plan.temp),
		slog.Any("steps", plan.steps),
	)

	done := 0
	for i, st := range plan.steps {
		if hook != nil {
			if hookErr := hook(i, st.From, st.To); hookErr != nil {
				err = &ExecError{Step: i, From: st.From, To: st.To, Err: hookErr}
				break
			}
		}
		if moveErr := ex.ns.moveLocked(st.From, st.To); moveErr != nil {
			err = &ExecError{Step: i, From: st.From, To: st.To, Err: moveErr}
			break
		}
		done = i + 1
		ex.logger.LogAttrs(ctx, slog.LevelDebug, "step applied",
			slog.Int("step", i),
			slog.String("from", st.From),
			slog.String("to", st.To),
		)
	}

	if err != nil {
		rollback := ex.rollbackLocked(plan.steps[:done])
		ex.ns.unlockBatch()
		ex.logger.LogAttrs(ctx, slog.LevelError, "step failed; batch rolled back",
			slog.Any("input", renames),
			slog.Int("failed_step", err.(*ExecError).Step),
			slog.Any("rolled_back_steps", rollback),
			slog.String("reason", err.Error()),
		)
		return err
	}

	var id int64
	if len(plan.steps) > 0 {
		id = ex.publishLocked(plan.steps)
	} else {
		ex.mu.Lock()
		if ex.last != nil {
			id = ex.last.id
		}
		ex.mu.Unlock()
	}
	snapshot := ex.ns.sortedSnapshotLocked()
	ex.ns.unlockBatch()

	ex.logger.LogAttrs(ctx, slog.LevelInfo, "batch committed",
		slog.Int64("batch_id", id),
		slog.Any("input", renames),
		slog.Any("output", snapshot),
		slog.Int("steps", len(plan.steps)),
		slog.Any("temporary_names", plan.temp),
		slog.String("basis", "chains executed from terminal to source; each cycle broken with exactly one temporary name"),
	)
	return nil
}

// rollbackLocked 在持锁状态下按逆序撤回已执行步骤，返回实际撤回动作。
func (ex *Executor) rollbackLocked(done []step) []step {
	rolled := make([]step, 0, len(done))
	for i := len(done) - 1; i >= 0; i-- {
		st := done[i]
		if err := ex.ns.moveLocked(st.To, st.From); err != nil {
			// 理论上不可达：每步都已成功且整个批次持锁，逆序一定合法。
			panic(fmt.Sprintf("rename: rollback failed at step %d (%s->%s): %v", i, st.From, st.To, err))
		}
		rolled = append(rolled, step{From: st.To, To: st.From})
	}
	return rolled
}

// publishLocked 登记成功批次。必须在 ns 写锁内调用，且与 ex.mu 形成固定顺序。
func (ex *Executor) publishLocked(steps []step) int64 {
	ex.mu.Lock()
	id := ex.nextID
	ex.nextID++
	ex.last = &batchRecord{id: id, steps: append([]step(nil), steps...)}
	ex.mu.Unlock()
	return id
}

// Undo 撤销最近一次成功批次。
//
// 已经撤销过、从未有成功批次、或最近一次成功批次之后又有新批次成功时拒绝。
// 撤销本身也在命名空间写锁内一次完成，撤销后命名空间逐项复原。
func (ex *Executor) Undo(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ex.submitMu.Lock()
	defer ex.submitMu.Unlock()

	ex.mu.Lock()
	rec := ex.last
	ex.mu.Unlock()
	if rec == nil {
		err := &UndoError{Reason: "nothing_to_undo", Detail: "no successful batch has been committed yet"}
		ex.logger.LogAttrs(ctx, slog.LevelWarn, "undo rejected", slog.String("reason", err.Reason))
		return err
	}

	ex.ns.lockBatch()
	rollback := ex.rollbackLocked(rec.steps)

	ex.mu.Lock()
	// last 仍指向同一批次才允许提交撤销结果；串行提交下这恒成立，
	// 这里做防御性确认。
	if ex.last != rec {
		ex.mu.Unlock()
		_ = ex.rollbackLocked(reverseSteps(rollback))
		ex.ns.unlockBatch()
		return &UndoError{Reason: "superseded", Detail: "a newer batch has been committed"}
	}
	ex.last = nil
	ex.mu.Unlock()

	snapshot := ex.ns.sortedSnapshotLocked()
	ex.ns.unlockBatch()

	ex.logger.LogAttrs(ctx, slog.LevelInfo, "batch undone",
		slog.Int64("batch_id", rec.id),
		slog.Any("output", snapshot),
		slog.Any("rolled_back_steps", rollback),
		slog.String("basis", "successful steps reversed in reverse order; namespace restored item by item"),
	)
	return nil
}

// LastBatchID 返回最近一次成功（且未撤销）批次的标识；没有时返回 0。
func (ex *Executor) LastBatchID() int64 {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if ex.last == nil {
		return 0
	}
	return ex.last.id
}

func reverseSteps(in []step) []step {
	out := make([]step, len(in))
	for i, st := range in {
		out[len(in)-1-i] = st
	}
	return out
}

func reasonOf(err error) string {
	if ve, ok := err.(*ValidationError); ok {
		return string(ve.Reason)
	}
	return "unknown"
}
