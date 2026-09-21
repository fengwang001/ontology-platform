package ontology

import "errors"

// ErrNonPositiveN is returned by New when Config.N is zero or negative.
var ErrNonPositiveN = errors.New("ontology: N must be a positive integer")

// Config configures a Selector.
type Config struct {
	// GroupKey is the column used to assign a row to a group.
	GroupKey string
	// ScoreKey is the column holding the numeric score to rank by.
	ScoreKey string
	// TieKey is the string column used to break score ties (ascending).
	TieKey string
	// N is the maximum number of rows retained per group. Must be > 0.
	N int
}

// New creates a Selector. It returns ErrNonPositiveN if cfg.N <= 0.
func New(cfg Config) (*Selector, error) {
	if cfg.N <= 0 {
		return nil, ErrNonPositiveN
	}
	return &Selector{
		cfg:    cfg,
		groups: make(map[string]*group),
	}, nil
}
