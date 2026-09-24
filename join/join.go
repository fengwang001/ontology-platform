// Package join incrementally maintains R(a,b) ⋈ S(b,c) ⋈ T(c,d) as a materialized multiset.
package join

import (
	"errors"
	"maps"
	"ontology/rel"
	"sync"
)

// Sentinel errors; callers discriminate with errors.Is. All three are distinct.
var (
	ErrBadTable = errors.New("join: unknown table (want R, S or T)")
	ErrNotFound = rel.ErrNotFound
	ErrLimit    = errors.New("join: result size exceeds maxResults")
)

type Quad struct{ A, B, C, D int }

// DB holds the tables, b/c indexes and materialized result; one mutex guards it all.
type DB struct {
	mu                             sync.Mutex
	r, s, t                        *rel.Rel
	sByB, sByC, rByB, tByC         map[int]map[rel.Tuple]int
	res                            map[Quad]int
	resLen, maxResults, lastProbes int
}

// New creates an empty database. maxResults <= 0 means unlimited.
func New(maxResults int) *DB {
	return &DB{
		r: rel.New(), s: rel.New(), t: rel.New(),
		sByB: map[int]map[rel.Tuple]int{}, sByC: map[int]map[rel.Tuple]int{},
		rByB: map[int]map[rel.Tuple]int{}, tByC: map[int]map[rel.Tuple]int{},
		res: map[Quad]int{}, maxResults: maxResults,
	}
}

// Insert adds one tuple (count +1) and its join contributions.
func (db *DB) Insert(table string, x rel.Tuple) error { return db.apply(table, x, true) }

// Delete removes one tuple (count -1) and all its join contributions.
func (db *DB) Delete(table string, x rel.Tuple) error { return db.apply(table, x, false) }

func (db *DB) Result() map[Quad]int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return maps.Clone(db.res)
}

// apply fully validates and only then commits; a rejected call changes no state.
func (db *DB) apply(table string, x rel.Tuple, insert bool) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	var tab *rel.Rel
	switch table { // validate table name before touching anything
	case "R":
		tab = db.r
	case "S":
		tab = db.s
	case "T":
		tab = db.t
	default:
		return ErrBadTable
	}
	if !insert && tab.Count(x) == 0 {
		return ErrNotFound
	}
	delta := 1
	if !insert {
		delta = -1
	}
	d, probes := db.delta(table, x, delta)
	net := 0
	for _, v := range d {
		net += v
	}
	if db.maxResults > 0 && db.resLen+net > db.maxResults {
		return ErrLimit
	}
	if insert { // commit relation count, mirrored indexes, then result multiset
		tab.Insert(x)
	} else {
		_ = tab.Delete(x) // existence verified above
	}
	switch table {
	case "R":
		idxAdd(db.rByB, x.Y, x, delta)
	case "S":
		idxAdd(db.sByB, x.X, x, delta)
		idxAdd(db.sByC, x.Y, x, delta)
	case "T":
		idxAdd(db.tByC, x.X, x, delta)
	}
	for q, v := range d {
		if c := db.res[q] + v; c == 0 {
			delete(db.res, q)
		} else {
			db.res[q] = c
		}
	}
	db.resLen += net
	db.lastProbes = probes
	return nil
}

// delta returns the signed result delta and candidate-pair probes (index lookups only).
func (db *DB) delta(table string, x rel.Tuple, d int) (map[Quad]int, int) {
	out, probes := map[Quad]int{}, 0
	switch table {
	case "R": // R(a,b) -> S by b -> T by each s.c
		for s, sc := range db.sByB[x.Y] {
			for t, tc := range db.tByC[s.Y] {
				probes++
				out[Quad{x.X, x.Y, s.Y, t.Y}] += d * sc * tc
			}
		}
	case "S": // S(b,c) -> R by b, T by c
		for r, rc := range db.rByB[x.X] {
			for t, tc := range db.tByC[x.Y] {
				probes++
				out[Quad{r.X, x.X, x.Y, t.Y}] += d * rc * tc
			}
		}
	case "T": // T(c,d) -> S by c -> R by each s.b
		for s, sc := range db.sByC[x.X] {
			for r, rc := range db.rByB[s.X] {
				probes++
				out[Quad{r.X, s.X, x.X, x.Y}] += d * rc * sc
			}
		}
	}
	return out, probes
}

func idxAdd(idx map[int]map[rel.Tuple]int, key int, x rel.Tuple, delta int) {
	bin := idx[key]
	if bin == nil {
		bin = map[rel.Tuple]int{}
		idx[key] = bin
	}
	if bin[x]+delta == 0 {
		delete(bin, x)
		if len(bin) == 0 {
			delete(idx, key)
		}
	} else {
		bin[x] += delta
	}
}
