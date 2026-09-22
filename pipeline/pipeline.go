// Package pipeline wires record ingestion, memory-budget accounting,
// background spilling, K-way merge and an atomic checkpoint into a
// crash-recoverable external sort.
//
// State and spill files live entirely under a caller-provided local
// working directory; nothing touches the network.
package pipeline

import (
	"os"
	"path/filepath"
	"sync"

	"ontology/budget"
	"ontology/record"
	"ontology/spill"
)

// Faults is the test-only fault injection surface. Nil means no faults.
type Faults struct {
	// CrashAfter is invoked just after each listed phase-boundary
	// transition is persisted: "spill" (final flush completed),
	// "merge" (a merge iteration boundary), "finalize" (output rename
	// done). Returning true aborts the process (os.Exit) — tests run
	// the binary in a subprocess. Simpler tests set it to a channel
	// callback; see crash_test.go.
	CrashAfter func(phase string) bool
	// TruncateRun, when non-nil, forces the next spill run file to be
	// closed after cut bytes (absolute file offset). Negative disables.
	TruncateRun func(runID uint64) int
}

// Pipeline is the external sort pipeline.
type Pipeline struct {
	dir    string
	budget *budget.Budget

	mu       sync.Mutex
	resident []record.Record
	resBytes uint64
	nextSeq  uint64
	runs     uint64
	closed   bool
	spillErr error

	flushMu  sync.Mutex // serializes spill runs
	stopSig  chan struct{}
	doneSig  chan struct{}
	jobs     chan ingestJob
	stopping bool
	waiting  []ingestJob

	faults *Faults

	comparisons  uint64 // read back through merge stats after Close
	peakResident uint64
}

// Config configures a Pipeline.
type Config struct {
	Dir        string
	MemoryByte uint64
	Faults     *Faults
}

// Open opens an existing work directory for recovery, or starts a new
// pipeline when no checkpoint exists yet.
func Open(cfg Config) (*Pipeline, error) {
	if cfg.MemoryByte == 0 && !hasCheckpoint(cfg.Dir) {
		return nil, budget.ErrInvalidLimit
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	cp, found, err := loadCheckpoint(cfg.Dir)
	if err != nil {
		return nil, err
	}
	limit := cfg.MemoryByte
	if found && limit == 0 {
		// Recovery callers may omit the limit; it is irrelevant on disk.
		limit = 1 << 20
	}
	b, err := budget.New(limit)
	if err != nil {
		return nil, err
	}
	p := &Pipeline{
		dir:     cfg.Dir,
		budget:  b,
		stopSig: make(chan struct{}),
		doneSig: make(chan struct{}),
		faults:  cfg.Faults,
	}
	if found {
		p.runs = cp.Runs
		p.nextSeq = cp.Accepted
		p.comparisons = cp.Comparisons
		p.peakResident = cp.PeakResident
		if err := p.recover(cp); err != nil {
			return nil, err
		}
		if cp.Phase != PhaseIngest {
			p.closed = true
			p.budget.Close()
		}
	}
	if !found || (found && cp.Phase == PhaseIngest) {
		p.jobs = make(chan ingestJob, 128)
		go p.runCoordinator()
	}
	return p, nil
}

func hasCheckpoint(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, checkpointName))
	return err == nil
}

var _ = spill.RunPath // keep imports stable while building
