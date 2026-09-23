// Package op defines the three-phase volcano operator interface.
package op

import (
	"context"
	"errors"
)

// ErrClosed is returned by Next when the operator was closed concurrently.
var ErrClosed = errors.New("op: operator closed")

// Row is a materialized tuple of string columns.
type Row struct {
	Vals []string
}

// Clone returns a deep copy of the row's backing slice.
func (r Row) Clone() Row {
	if r.Vals == nil {
		return Row{}
	}
	cp := make([]string, len(r.Vals))
	copy(cp, r.Vals)
	return Row{Vals: cp}
}

// Operator is the Open/Next/Close lifecycle contract.
//
// Open prepares resources (and opens children bottom-up). Next returns the
// next row; ok==false with err==nil means the stream is exhausted. Close
// releases resources exactly once and is idempotent.
type Operator interface {
	Open(ctx context.Context) error
	Next(ctx context.Context) (row Row, ok bool, err error)
	Close() error
}

// Drain consumes an opened operator until exhaustion and returns the rows.
// It never closes the operator; the caller owns Close.
func Drain(ctx context.Context, o Operator) ([]Row, error) {
	var rows []Row
	for {
		r, ok, err := o.Next(ctx)
		if err != nil {
			return rows, err
		}
		if !ok {
			return rows, nil
		}
		rows = append(rows, r)
	}
}
