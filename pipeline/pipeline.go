// Package pipeline orchestrates the external-sort state machine
// Ingest -> Spill -> Merge -> Finalize with checkpointed crash recovery.
package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"ontology/budget"
	"ontology/record"
	"ontology/spill"
)

// Sentinel errors returned by the pipeline.
var (
	ErrClosed   = errors.New("pipeline: closed")
	ErrCrashed  = errors.New("pipeline: simulated crash")
	ErrTooLarge = budget.ErrTooLarge
)

// CrashPoint identifies a test-only crash injection point at a stage
// boundary.
type CrashPoint int

// Crash injection points.
const (
	CrashNone           CrashPoint = iota // no injection
	CrashAfterSpill                       // after Spill, before Merge
	CrashMidMerge                         // halfway through Merge
	CrashBeforeFinalize                   // before Finalize
)

type spillTask struct {
	recs  []record.Record
	bytes int64
}

// Pipeline is the external-sort orchestrator. Not safe to copy.
type Pipeline struct {
	dir      string
	b        *budget.Budget
	crash    CrashPoint
	mu       sync.Mutex // guards buf, seq, closed and spillCh send/close
	buf      []record.Record
	bufBytes int64
	seq      uint64
	closed   bool
	spillCh  chan spillTask
	chClosed bool

	stateMu  sync.Mutex // guards runs, ingested, stage
	runs     []string
	ingested int64
	stage    Stage
	wg       sync.WaitGroup
	mergeCmp int64 // comparisons of the last merge
}

// Open creates or recovers a pipeline rooted at dir with the given
// resident-byte limit. If a checkpoint exists, state is loaded from disk.
func Open(dir string, limit int64) (*Pipeline, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := &Pipeline{
		dir:     dir,
		b:       budget.New(limit),
		stage:   StageIngest,
		spillCh: make(chan spillTask, 1),
	}
	if err := p.loadCheckpoint(); err != nil {
		return nil, err
	}
	p.seq = uint64(p.ingested)
	if p.stage != StageIngest {
		p.closed = true
	}
	p.wg.Add(1)
	go p.spillWorker()
	return p, nil
}

// SetCrashPoint installs a test-only crash injection point.
func (p *Pipeline) SetCrashPoint(cp CrashPoint) { p.crash = cp }

// Ingest adds one record. It blocks while the budget is exhausted and
// returns ErrClosed after Close, ErrTooLarge for oversized records.
func (p *Pipeline) Ingest(key string, value []byte) error {
	size := int64(record.Record{Key: key, Value: value}.EncodedLen())
	for {
		ok, err := p.b.TryAcquire(size)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		// Budget exhausted: spill the current buffer to free bytes.
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return ErrClosed
		}
		t := p.swapLocked()
		p.sendLocked(t)
		p.mu.Unlock()
		if len(t.recs) == 0 {
			// Budget is held by in-flight spills; wait for the worker.
			if err := p.b.Acquire(size); err != nil {
				return err
			}
			break
		}
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.b.Release(size)
		return ErrClosed
	}
	p.seq++
	p.buf = append(p.buf, record.Record{Key: key, Value: value, Seq: p.seq})
	p.bufBytes += size
	if p.bufBytes >= p.b.Limit() {
		p.sendLocked(p.swapLocked())
	}
	p.mu.Unlock()
	return nil
}

// swapLocked detaches the current buffer. Caller holds mu.
func (p *Pipeline) swapLocked() spillTask {
	t := spillTask{recs: p.buf, bytes: p.bufBytes}
	p.buf = nil
	p.bufBytes = 0
	return t
}

// sendLocked queues t for the spill worker. Caller holds mu; the worker
// never takes mu, so a blocking send cannot deadlock.
func (p *Pipeline) sendLocked(t spillTask) {
	if len(t.recs) != 0 {
		p.spillCh <- t
	}
}

func (p *Pipeline) spillWorker() {
	defer p.wg.Done()
	for t := range p.spillCh {
		sort.Slice(t.recs, func(i, j int) bool { return record.Less(t.recs[i], t.recs[j]) })
		name := fmt.Sprintf("run-%d.osrt", t.recs[0].Seq)
		if err := p.writeRun(name, t.recs); err == nil {
			p.stateMu.Lock()
			p.runs = append(p.runs, name)
			p.ingested += int64(len(t.recs))
			_ = p.saveCheckpointLocked()
			p.stateMu.Unlock()
		}
		p.b.Release(t.bytes)
	}
}

func (p *Pipeline) writeRun(name string, recs []record.Record) error {
	path := filepath.Join(p.dir, name)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	werr := spill.WriteRun(f, recs)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(tmp, path)
}

// Close stops ingestion, spills the remaining buffer and runs the
// remaining stages. On a recovered pipeline it resumes the state machine.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.sendLocked(p.swapLocked())
	}
	if !p.chClosed {
		p.chClosed = true
		close(p.spillCh)
	}
	p.mu.Unlock()
	p.wg.Wait()
	return p.runStages()
}
