package importer

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

var (
	// ErrUnknownJob 表示块或查询指向一个不存在的任务。
	ErrUnknownJob = errors.New("importer: unknown job")
	// ErrJobExists 表示重复创建相同标识的任务。
	ErrJobExists = errors.New("importer: job already exists")
	// ErrChunkConflict 表示块序号重复且内容不一致。
	ErrChunkConflict = errors.New("importer: duplicate chunk sequence with different content")
	// ErrJobTerminated 表示任务已提前结束，块被拒绝。
	ErrJobTerminated = errors.New("importer: job terminated, chunk rejected")
	// ErrSeqOutOfRange 表示块序号超出任务声明的块数上限范围。
	ErrSeqOutOfRange = errors.New("importer: chunk sequence out of declared range")
)

// Validator 校验单个条目，返回非 nil 错误表示条目自身校验失败。
type Validator func(Entry) error

// Engine 是批量导入子系统的主入口，管理多个导入任务。
//
// 并发模型：不同任务的块完全并行；同一任务内，块受理（重复判定、
// 提前结束判定）在任务锁内完成，条目校验在锁外并行执行，状态提交
// 重新进入任务锁。因此无依赖关系的并发块可以并行处理且互不影响，
// 而所有状态变更都经过任务锁串行化，保证结果等价于某个确定顺序。
type Engine struct {
	mu        sync.RWMutex
	jobs      map[string]*job
	validator Validator
	log       *slog.Logger
}

// Option 配置 Engine。
type Option func(*Engine)

// WithValidator 设置条目校验器。
func WithValidator(v Validator) Option {
	return func(e *Engine) { e.validator = v }
}

// WithLogger 设置日志器，引擎会打印每个块与条目的输入、处理结果与判定依据。
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) { e.log = l }
}

// NewEngine 创建批量导入引擎。
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		jobs: make(map[string]*job),
		validator: func(ent Entry) error {
			if ent.ID == "" {
				return errors.New("importer: empty entry id")
			}
			return nil
		},
		log: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// CreateJob 创建一个导入任务。maxChunks 为任务声明的总块数上限，
// 取 0 表示不声明上限（悬挂条目可能无限期等待）。
func (e *Engine) CreateJob(id string, maxChunks int) error {
	if maxChunks < 0 {
		return fmt.Errorf("importer: negative maxChunks %d", maxChunks)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.jobs[id]; ok {
		return ErrJobExists
	}
	e.jobs[id] = newJob(id, maxChunks, e.validator, e.log)
	e.log.Info("job created", "job", id, "max_chunks", maxChunks)
	return nil
}

// SubmitChunk 提交一个数据块。
//
// 受理判定按错误类别优先级从高到低进行：内容不一致的重复块永远优先
// 于提前结束拒绝被报告。内容一致的重复块是幂等空操作，即使任务已
// 提前结束也返回 OutcomeDuplicate，不影响任何已确定结果。
func (e *Engine) SubmitChunk(c Chunk) SubmitResult {
	e.mu.RLock()
	j, ok := e.jobs[c.JobID]
	e.mu.RUnlock()
	if !ok {
		return SubmitResult{Outcome: OutcomeRejected, Err: ErrUnknownJob}
	}
	sum := c.Hash()

	// 受理阶段：在任务锁内完成，保证与 TerminateJob 之间的线性化。
	j.mu.Lock()
	if rec, seen := j.chunks[c.Seq]; seen {
		if rec.hash == sum {
			j.mu.Unlock()
			j.log.Info("chunk duplicate ignored",
				"job", c.JobID, "seq", c.Seq, "reason", "same sequence and content hash already processed")
			return SubmitResult{Outcome: OutcomeDuplicate}
		}
		marked := j.markConflictLocked(c)
		j.mu.Unlock()
		j.log.Warn("chunk conflict rejected",
			"job", c.JobID, "seq", c.Seq, "new_conflict_entries", marked,
			"reason", "same sequence with different content; previously determined results are final")
		return SubmitResult{
			Outcome:  OutcomeRejected,
			Category: CategoryChunkConflict,
			Err:      fmt.Errorf("%w: job %q seq %d", ErrChunkConflict, c.JobID, c.Seq),
		}
	}
	if c.Seq < 0 || (j.maxChunks > 0 && c.Seq >= j.maxChunks) {
		j.mu.Unlock()
		j.log.Warn("chunk rejected", "job", c.JobID, "seq", c.Seq,
			"reason", "sequence out of declared range", "max_chunks", j.maxChunks)
		return SubmitResult{
			Outcome:  OutcomeRejected,
			Category: CategoryEntryInvalid,
			Err:      fmt.Errorf("%w: job %q seq %d not in [0,%d)", ErrSeqOutOfRange, c.JobID, c.Seq, j.maxChunks),
		}
	}
	if j.terminated {
		j.mu.Unlock()
		j.log.Warn("chunk rejected", "job", c.JobID, "seq", c.Seq,
			"reason", "job terminated early")
		return SubmitResult{
			Outcome:  OutcomeRejected,
			Category: CategoryJobTerminated,
			Err:      fmt.Errorf("%w: job %q seq %d", ErrJobTerminated, c.JobID, c.Seq),
		}
	}
	j.chunks[c.Seq] = chunkRecord{hash: sum}
	j.arrived++
	j.inflight++
	j.mu.Unlock()

	// 校验阶段：在锁外执行，不同块的校验可以并行。
	valErrs := make([]error, len(c.Entries))
	for i, ent := range c.Entries {
		valErrs[i] = j.validator(ent)
	}

	// 提交阶段：重新进入任务锁，基于最新状态确定条目结果。
	j.mu.Lock()
	j.commitLocked(c, valErrs)
	j.inflight--
	j.mu.Unlock()
	return SubmitResult{Outcome: OutcomeAccepted}
}

// TerminateJob 将任务显式标记为提前结束。
//
// 标记生效前已被受理的块会继续处理完毕；生效后到达的新块被拒绝。
// 标记与块受理都在任务锁内完成，二者存在确定的先后顺序，最终结果
// 等价于按该顺序串行执行，不存在竞态导致的不确定结果。
func (e *Engine) TerminateJob(id string) error {
	e.mu.RLock()
	j, ok := e.jobs[id]
	e.mu.RUnlock()
	if !ok {
		return ErrUnknownJob
	}
	j.mu.Lock()
	j.terminated = true
	j.mu.Unlock()
	e.log.Info("job terminated early", "job", id)
	return nil
}

// QueryEntry 查询条目当前状态。查询是只读操作，不改变任何处理状态。
func (e *Engine) QueryEntry(jobID, entryID string) EntryInfo {
	e.mu.RLock()
	j, ok := e.jobs[jobID]
	e.mu.RUnlock()
	if !ok {
		return EntryInfo{Status: StatusUnknown}
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	es, ok := j.entries[entryID]
	if !ok {
		return EntryInfo{Status: StatusUnknown}
	}
	return EntryInfo{Status: es.status, Category: es.category, Detail: es.detail}
}

// Stats 返回任务内部状态的只读快照。
func (e *Engine) Stats(jobID string) (Stats, bool) {
	e.mu.RLock()
	j, ok := e.jobs[jobID]
	e.mu.RUnlock()
	if !ok {
		return Stats{}, false
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	dangling := 0
	for _, set := range j.waiters {
		dangling += len(set)
	}
	return Stats{
		ArrivedChunks: j.arrived,
		Landed:        j.landed,
		Failed:        j.failed,
		Timeout:       j.timedOut,
		Pending:       len(j.pending),
		Conflict:      j.conflict,
		DanglingRefs:  dangling,
		EvalOps:       j.evalOps,
		ScanOps:       j.scanOps,
	}, true
}
