// Package api is the public facade over djoin: New, Feed, View, SelfCheck.
package api

import (
	"ontology/djoin"
	"ontology/rel"
)

// Row is one signed change: Sign=+1 inserts, Sign=-1 deletes one occurrence.
type Row = rel.Row

// Delta is one signed, non-zero multiplicity change of one output tuple.
type Delta = djoin.Delta

// Joiner is the incremental inner join engine.
type Joiner = djoin.Engine

// The three distinct, decidable sentinel errors.
var (
	ErrInvalidChange = djoin.ErrInvalidChange
	ErrDeleteMissing = djoin.ErrDeleteMissing
	ErrViewLimit     = djoin.ErrViewLimit
)

// New creates a joiner whose materialized view holds at most maxView tuples.
func New(maxView int) *Joiner { return djoin.New(maxView) }
