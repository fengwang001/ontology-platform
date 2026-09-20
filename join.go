package ontology

import (
	"errors"
	"sort"
)

// Mode selects the join semantics.
type Mode int

const (
	// Inner emits only matched left/right pairs.
	Inner Mode = iota
	// Left emits matched pairs plus every unmatched left row.
	Left
)

// Stats reports measurable facts about one Join call.
type Stats struct {
	// OutputRows is the number of rows the join produced.
	OutputRows int
	// MaxFanOut is the largest per-key expansion factor m*n (left rows
	// times right rows sharing one key); 0 when nothing matched.
	MaxFanOut int
	// LeftNullKey counts left rows that went unmatched because at least
	// one join key was empty (missing, nil, or NaN).
	LeftNullKey int
	// LeftNoPartner counts left rows that went unmatched because their
	// (non-empty) key had no partner on the right.
	LeftNoPartner int
}

// pair is one matched left/right combination awaiting output.
type pair struct {
	key        []keyVal
	left       Row
	right      Row
	leftCanon  string
	rightCanon string
}

// rightGroup holds all right rows sharing one encoded key.
type rightGroup struct {
	rows   []Row
	canons []string
}

// Join equi-joins left and right on the ordered join keys. The result
// order is fully deterministic and independent of input order; see the
// package documentation for the exact ordering, naming, and NULL rules.
func Join(left, right []Row, keys []string, mode Mode) ([]Row, Stats, error) {
	var stats Stats
	if len(keys) == 0 {
		return nil, stats, errors.New("ontology: at least one join key is required")
	}
	if err := validateKeyTypes(left, right, keys); err != nil {
		return nil, stats, err
	}

	groups := make(map[string]*rightGroup)
	tuples := make(map[string][]keyVal)
	for _, r := range right {
		tuple, null, err := extractKey(r, keys)
		if err != nil {
			return nil, stats, err
		}
		if null {
			continue // NULL keys never match, not even each other
		}
		enc := encodeKeyTuple(tuple)
		g := groups[enc]
		if g == nil {
			g = &rightGroup{}
			groups[enc] = g
			tuples[enc] = tuple
		}
		g.rows = append(g.rows, r)
		g.canons = append(g.canons, canonicalRow(r))
	}

	leftCounts := make(map[string]int)
	for _, l := range left {
		tuple, null, err := extractKey(l, keys)
		if err != nil {
			return nil, stats, err
		}
		if !null {
			leftCounts[encodeKeyTuple(tuple)]++
		}
	}
	for enc, g := range groups {
		if n := leftCounts[enc]; n > 0 {
			if fanOut := n * len(g.rows); fanOut > stats.MaxFanOut {
				stats.MaxFanOut = fanOut
			}
		}
	}

	var matched []pair
	var unmatched []pair
	for _, l := range left {
		tuple, null, err := extractKey(l, keys)
		if err != nil {
			return nil, stats, err
		}
		if null {
			stats.LeftNullKey++
			unmatched = append(unmatched, pair{left: l, leftCanon: canonicalRow(l)})
			continue
		}
		g := groups[encodeKeyTuple(tuple)]
		if g == nil {
			stats.LeftNoPartner++
			unmatched = append(unmatched, pair{left: l, leftCanon: canonicalRow(l)})
			continue
		}
		for i, r := range g.rows {
			matched = append(matched, pair{
				key:        tuple,
				left:       l,
				right:      r,
				leftCanon:  canonicalRow(l),
				rightCanon: g.canons[i],
			})
		}
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if c := compareKeyTuples(matched[i].key, matched[j].key); c != 0 {
			return c < 0
		}
		if matched[i].leftCanon != matched[j].leftCanon {
			return matched[i].leftCanon < matched[j].leftCanon
		}
		return matched[i].rightCanon < matched[j].rightCanon
	})
	sort.SliceStable(unmatched, func(i, j int) bool {
		return unmatched[i].leftCanon < unmatched[j].leftCanon
	})

	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var out []Row
	if mode == Inner {
		out = make([]Row, 0, len(matched))
	} else {
		out = make([]Row, 0, len(matched)+len(unmatched))
	}
	for _, p := range matched {
		out = append(out, buildRow(p.left, p.right, keySet))
	}
	if mode == Left {
		for _, p := range unmatched {
			out = append(out, deepCopyRow(p.left))
		}
	}
	stats.OutputRows = len(out)
	return out, stats, nil
}

// buildRow merges one matched pair into a fresh result row. Left values win
// name collisions; colliding right attributes are renamed "right."+name.
func buildRow(l, r Row, keySet map[string]bool) Row {
	out := deepCopyRow(l)
	for name, v := range r {
		if keySet[name] {
			continue // join keys are taken from the left row
		}
		target := name
		if _, exists := out[name]; exists {
			target = "right." + name
		}
		out[target] = deepCopyValue(v)
	}
	return out
}
