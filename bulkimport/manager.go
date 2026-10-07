package bulkimport

import (
	"errors"
	"io"
	"log/slog"
	"sync"
)

// ErrUnknownJob is returned by operations naming a job that does not
// exist.
var ErrUnknownJob = errors.New("bulkimport: unknown job")

// ErrJobExists is returned by CreateJob when the job ID is taken.
var ErrJobExists = errors.New("bulkimport: job already exists")

// ErrEmptyJobID is returned by CreateJob for an empty job ID.
var ErrEmptyJobID = errors.New("bulkimport: empty job id")

// Manager owns all import jobs.
type Manager struct {
	mu     sync.Mutex
	jobs   map[string]*job
	logger *slog.Logger
}

// Option customizes a Manager.
type Option func(*Manager)

// WithLogger sets the logger receiving per-chunk and per-entry
// decisions. The default logger discards everything.
func WithLogger(l *slog.Logger) Option {
	return func(m *Manager) {
		if l != nil {
			m.logger = l
		}
	}
}

// NewManager creates a Manager.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		jobs:   make(map[string]*job),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// CreateJob registers a new import job.
func (m *Manager) CreateJob(cfg JobConfig) error {
	if cfg.ID == "" {
		return ErrEmptyJobID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[cfg.ID]; ok {
		return ErrJobExists
	}
	m.jobs[cfg.ID] = newJob(cfg, m.logger.With("job", cfg.ID))
	return nil
}

func (m *Manager) getJob(id string) (*job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrUnknownJob
	}
	return j, nil
}

// SubmitChunk presents one chunk to its job. The outcome is
// deterministic: new chunks are accepted and processed, identical
// resubmissions are duplicates, and everything else is rejected with a
// classified error.
func (m *Manager) SubmitChunk(c Chunk) (ChunkResult, error) {
	j, err := m.getJob(c.JobID)
	if err != nil {
		return ChunkResult{}, err
	}
	return j.submit(c), nil
}

// CloseJob marks a job as finished early. Chunks that were already
// accepted still finish processing; newly arriving chunks are rejected
// with ErrKindJobClosed. Concurrent SubmitChunk calls are serialized
// against CloseJob, so the final state always equals one of the two
// possible sequential orders.
func (m *Manager) CloseJob(id string) error {
	j, err := m.getJob(id)
	if err != nil {
		return err
	}
	j.close()
	return nil
}

// QueryEntry returns a read-only snapshot of an entry's state. It never
// mutates job or chunk state.
func (m *Manager) QueryEntry(jobID, entryID string) (EntryView, error) {
	j, err := m.getJob(jobID)
	if err != nil {
		return EntryView{}, err
	}
	return j.queryEntry(entryID), nil
}

// Stats returns a snapshot of job counters.
func (m *Manager) Stats(jobID string) (Stats, error) {
	j, err := m.getJob(jobID)
	if err != nil {
		return Stats{}, err
	}
	return j.statsSnapshot(), nil
}
