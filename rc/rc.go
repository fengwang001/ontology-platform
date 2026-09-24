// Package rc implements read_committed consumption on top of txlog.
package rc

import (
	"errors"

	"ontology/txlog"
)

// ErrInvalidFrom is returned when from is negative or greater than the LSO.
var ErrInvalidFrom = errors.New("rc: fetch offset must be within [0, LSO]")

// Read returns committed data records in [from, LSO) in offset order and the
// next offset (the current LSO). Control markers and aborted or undecided
// transactions are never returned. The whole scan runs on one locked
// snapshot, so HW/LSO and record outcomes cannot change underneath it.
func Read(l *txlog.Log, from int) (vals []string, next int, err error) {
	s := l.OpenSnapshot()
	defer s.Close()
	if from < 0 || from > s.LSO { // validate against the locked LSO
		return nil, 0, ErrInvalidFrom
	}
	vals = []string{}
	for i := from; i < s.LSO; i++ {
		r, committed := s.At(i)
		if r.Kind == txlog.KindData && committed {
			vals = append(vals, r.Val)
		}
	}
	return vals, s.LSO, nil
}
