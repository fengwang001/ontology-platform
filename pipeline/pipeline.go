// Package pipeline 编排外部排序状态机 Ingest -> Spill -> Merge -> Finalize，
// 通过检查点文件支持从阶段边界崩溃后恢复。
package pipeline

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"ontology/budget"
	"ontology/merge"
	"ontology/record"
)

// Stage 是状态机阶段。
type Stage string

const (
	StageIngesting Stage = "ingesting"
	StageSpilled   Stage = "spilled"
	StageDone      Stage = "done"
)

// CrashPoint 标识可注入崩溃的阶段边界（仅测试使用）。
type CrashPoint int

const (
	CrashAfterSpill CrashPoint = iota
	CrashDuringMerge
	CrashBeforeFinalize
)

var (
	ErrClosed         = errors.New("pipeline: closed")
	ErrRecordTooLarge = errors.New("pipeline: record exceeds budget limit")
	ErrCrashInjected  = errors.New("pipeline: crash injected")
)

// Options 可选配置；CrashHook 仅用于测试的故障注入。
type Options struct {
	CrashHook func(CrashPoint) error
}

// Pipeline 是外部排序管线。Ingest 可并发调用。
type Pipeline struct {
	dir    string
	b      *budget.Budget
	hook   func(CrashPoint) error
	merger *merge.Merger

	mu       sync.Mutex
	closed   bool
	state    Stage
	buf      []record.Record
	bufBytes int64
	runs     []string
	ingested int64
	nextSeq  uint64
}

// Open 打开（或恢复）dir 上的管线。发现上次崩溃在 Spilled 状态时，
// 先修复 run 的最大可恢复前缀，再重新归并并 Finalize。
func Open(dir string, budgetBytes int64, opts *Options) (*Pipeline, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := &Pipeline{dir: dir, b: budget.New(budgetBytes), state: StageIngesting, merger: merge.NewMerger()}
	cp, err := p.loadCheckpoint()
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		p.state, p.runs, p.ingested, p.nextSeq = cp.State, cp.Runs, cp.Ingested, cp.NextSeq
	}
	if p.state == StageSpilled {
		p.mu.Lock()
		err = p.mergeLocked()
		if err == nil {
			err = os.Rename(p.tmpOutput(), p.OutputPath())
		}
		if err == nil {
			p.state = StageDone
			err = p.saveCheckpointLocked()
		}
		p.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	if opts != nil {
		p.hook = opts.CrashHook
	}
	return p, nil
}

// Ingest 接收一条记录；分配全局到达序号，超预算时先溢写再入缓冲。
func (p *Pipeline) Ingest(key string, value []byte) error {
	rec := record.Record{Key: key, Value: append([]byte(nil), value...)}
	size := int64(rec.Size())
	if size > p.b.Limit() {
		return ErrRecordTooLarge
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if err := p.b.TryAcquire(size); err != nil {
		if err := p.flushLocked(); err != nil {
			return err
		}
		if err := p.b.TryAcquire(size); err != nil {
			return err
		}
	}
	rec.Seq = p.nextSeq
	p.nextSeq++
	p.buf = append(p.buf, rec)
	p.bufBytes += size
	p.ingested++
	return nil
}

// Close 执行 Spill -> Merge -> Finalize；之后的 Ingest 返回 ErrClosed。
func (p *Pipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	p.closed = true
	if err := p.flushLocked(); err != nil {
		return err
	}
	p.state = StageSpilled
	if err := p.saveCheckpointLocked(); err != nil {
		return err
	}
	if err := p.callHook(CrashAfterSpill); err != nil {
		return err
	}
	if err := p.mergeLocked(); err != nil {
		return err
	}
	if err := p.callHook(CrashBeforeFinalize); err != nil {
		return err
	}
	if err := os.Rename(p.tmpOutput(), p.OutputPath()); err != nil {
		return err
	}
	p.state = StageDone
	return p.saveCheckpointLocked()
}

func (p *Pipeline) callHook(pt CrashPoint) error {
	if p.hook != nil && p.hook(pt) != nil {
		return ErrCrashInjected
	}
	return nil
}

func (p *Pipeline) tmpOutput() string { return filepath.Join(p.dir, "output.tmp") }

// OutputPath 返回最终有序输出文件路径。
func (p *Pipeline) OutputPath() string { return filepath.Join(p.dir, "output.run") }

// MaxResident 读出历史最大驻留字节数（非导出计数器 budget.max）。
func (p *Pipeline) MaxResident() int64 { return p.b.Max() }

// ResidentLimit 返回驻留字节硬上限。
func (p *Pipeline) ResidentLimit() int64 { return p.b.Limit() }

// Compares 返回归并阶段的键比较次数。
func (p *Pipeline) Compares() int64 { return p.merger.Compares() }

// RunCount 返回溢写 run 文件数。
func (p *Pipeline) RunCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.runs)
}

// Ingested 返回成功接收的记录数。
func (p *Pipeline) Ingested() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ingested
}
