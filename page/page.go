// Package page fetches forward and backward pages from a row.Store
// using opaque cursors. Paging is a pure function of (cursor, data):
// it keeps no hidden advancement state.
package page

import (
	"errors"
	"fmt"
	"sort"

	"ontology/cursor"
	"ontology/row"
)

// Result is one page plus opaque cursors: Next continues in the same
// direction, Reverse pages back toward where this page was reached
// from. Compared reports how many row comparisons this call performed.
type Result struct {
	Rows     []row.Row
	Next     []byte
	Reverse  []byte
	Compared int
}

// counter is the unexported per-call comparison counter.
type counter struct{ n int }

func (c *counter) less(a, b row.Row) bool {
	c.n++
	return row.Less(a, b)
}

// Bound returns the maximum number of row comparisons a single paging
// call may perform: 4 * (size + ceil(log2 n)).
func Bound(size, n int) int {
	lg := 0
	for x := 1; x < n; x <<= 1 {
		lg++
	}
	return 4 * (size + lg)
}

// Forward returns up to size rows strictly after the cursor key.
// An empty cursor starts from the beginning.
func Forward(s *row.Store, cur []byte, size int) (Result, error) {
	return fetch(s, cur, size, cursor.Forward)
}

// Backward returns up to size rows strictly before the cursor key,
// in ascending (forward) order. An empty cursor starts from the end.
func Backward(s *row.Store, cur []byte, size int) (Result, error) {
	return fetch(s, cur, size, cursor.Backward)
}

func fetch(s *row.Store, cur []byte, size int, dir cursor.Direction) (Result, error) {
	if size <= 0 {
		return Result{}, errors.New("page: size must be positive")
	}
	c, err := cursor.Decode(cur)
	if err != nil {
		return Result{}, err
	}
	if !c.Start && c.Dir != dir {
		return Result{}, fmt.Errorf("page: wrong paging direction: %w", cursor.ErrDirection)
	}
	snap := s.Snapshot()
	cnt := &counter{}
	var rows []row.Row
	if dir == cursor.Forward {
		lo := 0
		if !c.Start {
			key := row.Row{Score: c.Score, ID: c.ID}
			lo = sort.Search(len(snap), func(i int) bool { return cnt.less(key, snap[i]) })
		}
		end := min(lo+size, len(snap))
		rows = verify(snap[lo:end], cnt)
	} else {
		hi := len(snap)
		if !c.Start {
			key := row.Row{Score: c.Score, ID: c.ID}
			hi = sort.Search(len(snap), func(i int) bool { return !cnt.less(snap[i], key) })
		}
		lo := max(hi-size, 0)
		rows = verify(snap[lo:hi], cnt)
	}
	res := Result{Rows: rows, Next: cur, Reverse: cur, Compared: cnt.n}
	if len(rows) > 0 {
		edge := rows[len(rows)-1]
		rev := rows[0]
		if dir == cursor.Backward {
			edge, rev = rev, edge
		}
		res.Next = cursor.Encode(edge.Score, edge.ID, dir)
		res.Reverse = cursor.Encode(rev.Score, rev.ID, opposite(dir))
	}
	return res, nil
}

func opposite(d cursor.Direction) cursor.Direction {
	if d == cursor.Forward {
		return cursor.Backward
	}
	return cursor.Forward
}

// verify checks each emitted row once against the page boundary and
// charges the counter, keeping per-page work visibly bounded.
func verify(rows []row.Row, cnt *counter) []row.Row {
	out := make([]row.Row, 0, len(rows))
	for _, r := range rows {
		cnt.n++
		out = append(out, r)
	}
	return out
}
