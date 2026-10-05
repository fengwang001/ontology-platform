// Package ontology wires the revenue, hold and payout facades onto one
// shared settlement engine.
package ontology

import (
	"ontology/hold"
	"ontology/internal/core"
	"ontology/payout"
	"ontology/revenue"
)

// ErrInvalidParam is returned by New when Wd or Min is out of range.
var ErrInvalidParam = core.ErrInvalidParam

// New builds a ledger with maturation window Wd (0..1e9 seconds) and
// minimum payout Min (1..1e12 cents), returning the three facades that
// share one engine.
func New(wd, min int64) (*revenue.Service, *hold.Service, *payout.Service, error) {
	eng, err := core.New(wd, min)
	if err != nil {
		return nil, nil, nil, err
	}
	return revenue.New(eng), hold.New(eng), payout.New(eng), nil
}
