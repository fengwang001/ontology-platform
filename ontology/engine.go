package ontology

import "math/big"

// GroupResult is the global state of one group.
type GroupResult struct {
	Group       string
	Sum         *big.Rat
	Count       int64
	ValueCounts map[string]int64
}

// Engine is the thread-safe global aggregation state.
type Engine struct{}

// NewEngine creates an Engine with the given group limit and logger.
func NewEngine(maxGroups int, logger Logger) *Engine { return nil }

// Submit validates a batch, aggregates it locally and merges it globally.
func (e *Engine) Submit(events []Event) error { return nil }

// Query returns one group's metrics.
func (e *Engine) Query(group string) (sum, avg *big.Rat, count, distinct int64, ok bool) {
	return
}

// Snapshot returns all groups in name order.
func (e *Engine) Snapshot() []GroupResult { return nil }

// SentBatches returns the number of batches merged into global state.
func (e *Engine) SentBatches() int64 { return 0 }

// RejectedBatches returns the number of rejected submissions.
func (e *Engine) RejectedBatches() int64 { return 0 }
