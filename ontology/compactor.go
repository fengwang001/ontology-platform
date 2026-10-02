package ontology

import (
	"math"
	"sort"
	"sync"
)

// maxOperand is the largest absolute value allowed for a Put value or Merge delta.
const maxOperand int64 = 1_000_000_000_000

// Kind enumerates the kinds of records kept in the run set.
type Kind int

const (
	KindPut Kind = iota + 1
	KindMerge
	KindDelete
)

// Record is one entry of the run set.
type Record struct {
	Key  string
	Seq  int64
	Kind Kind
	Val  int64
}

// Stats reports the accounting of a single Compact call.
type Stats struct {
	In          int
	Out         int
	Shadowed    int
	Folded      int
	TombDropped int
	MergeToPut  int
}

// Latest selects the newest view in Get.
const Latest uint64 = ^uint64(0)

// Compactor is the snapshot-aware compaction iterator.
//
// All exported methods are safe for concurrent use. Compact is a single
// atomic step: no observer ever sees a partially rewritten run set.
type Compactor struct {
	mu      sync.Mutex
	deeper  map[string]int64
	records []Record // always sorted by Seq ascending
	maxSeq  int64
	holds   map[int64]int

	// stripeProbes counts binary-search comparisons used while assigning
	// records to stripes, over the lifetime of the Compactor.
	stripeProbes int
}

// New returns a Compactor whose deeper layers hold the given bases.
// The map is copied; its zero values are significant (presence is enough).
func New(deeper map[string]int64) *Compactor {
	c := &Compactor{holds: make(map[int64]int)}
	if len(deeper) > 0 {
		c.deeper = make(map[string]int64, len(deeper))
		for k, v := range deeper {
			c.deeper[k] = v
		}
	}
	return c
}

func validValue(v int64) bool {
	return v >= -maxOperand && v <= maxOperand
}

// Put appends a Put record and returns its sequence.
func (c *Compactor) Put(k string, v int64) (int64, error) {
	if k == "" {
		return 0, ErrEmptyKey
	}
	if !validValue(v) {
		return 0, ErrRange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxSeq++
	c.records = append(c.records, Record{Key: k, Seq: c.maxSeq, Kind: KindPut, Val: v})
	return c.maxSeq, nil
}

// Merge appends a Merge record (add d) and returns its sequence.
func (c *Compactor) Merge(k string, d int64) (int64, error) {
	if k == "" {
		return 0, ErrEmptyKey
	}
	if !validValue(d) {
		return 0, ErrRange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxSeq++
	c.records = append(c.records, Record{Key: k, Seq: c.maxSeq, Kind: KindMerge, Val: d})
	return c.maxSeq, nil
}

// Delete appends a Delete record and returns its sequence.
func (c *Compactor) Delete(k string) (int64, error) {
	if k == "" {
		return 0, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxSeq++
	c.records = append(c.records, Record{Key: k, Seq: c.maxSeq, Kind: KindDelete})
	return c.maxSeq, nil
}

// Snapshot returns the current maximum sequence and records one hold on it.
func (c *Compactor) Snapshot() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holds[c.maxSeq]++
	return c.maxSeq
}

// Release cancels one hold on s. It is an error if s is not held.
func (c *Compactor) Release(s int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.holds[s] <= 0 {
		return ErrNotHeld
	}
	c.holds[s]--
	if c.holds[s] == 0 {
		delete(c.holds, s)
	}
	return nil
}

// Records returns an ascending-by-sequence copy of the current run set.
func (c *Compactor) Records() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Record, len(c.records))
	copy(out, c.records)
	return out
}

// Get reads key k at held snapshot s, or at Latest.
func (c *Compactor) Get(k string, s uint64) (int64, bool, error) {
	if k == "" {
		return 0, false, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s != Latest {
		if c.holds[int64(s)] == 0 {
			return 0, false, ErrNotHeld
		}
	}
	var sum int64
	merged := false
	for i := len(c.records) - 1; i >= 0; i-- {
		e := c.records[i]
		if e.Key != k || uint64(e.Seq) > s {
			continue
		}
		switch e.Kind {
		case KindPut:
			return sum + e.Val, true, nil
		case KindDelete:
			if merged {
				return sum, true, nil
			}
			return 0, false, nil
		case KindMerge:
			sum += e.Val
			merged = true
		}
	}
	if base, ok := c.deeper[k]; ok {
		return sum + base, true, nil
	}
	return sum, merged, nil
}

// stripeBoundaries returns the ascending distinct stripe boundary set
// S union {Latest}, using MaxInt64 as the sentinel for Latest (every record
// sequence is at most MaxInt64).
func (c *Compactor) stripeBoundaries() []int64 {
	b := make([]int64, 0, len(c.holds)+1)
	for s := range c.holds {
		b = append(b, s)
	}
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	return append(b, math.MaxInt64)
}

// findStripe returns the smallest boundary >= q via binary search. Every
// comparison is charged to stripeProbes.
func (c *Compactor) findStripe(bounds []int64, q int64) int64 {
	lo, hi := 0, len(bounds)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		c.stripeProbes++
		if bounds[mid] < q {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return bounds[lo]
}

// Compact rewrites the run set once, key by key and stripe by stripe, and
// returns the exact accounting. The replacement is installed atomically.
func (c *Compactor) Compact() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	in := len(c.records)
	bounds := c.stripeBoundaries()

	byKey := make(map[string][]Record)
	for _, e := range c.records {
		byKey[e.Key] = append(byKey[e.Key], e)
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Record
	var shadowed, folded, tombs, mergeToPut int

	for _, k := range keys {
		recs := byKey[k] // ascending by Seq; stripes are processed newest first

		// Group into stripes, preserving newest-first order inside each stripe.
		groups := make(map[int64][]Record)
		var order []int64
		for i := len(recs) - 1; i >= 0; i-- {
			e := recs[i]
			st := c.findStripe(bounds, e.Seq)
			if _, ok := groups[st]; !ok {
				order = append(order, st)
			}
			groups[st] = append(groups[st], e)
		}

		var produced []Record // newest first
		for _, st := range order {
			g := groups[st] // newest first
			e1 := g[0]
			if e1.Kind == KindPut || e1.Kind == KindDelete {
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: e1.Kind, Val: e1.Val})
				shadowed += len(g) - 1
				continue
			}
			// e1 is Merge: accumulate the consecutive leading merges.
			var sum int64
			j := 0
			for j < len(g) && g[j].Kind == KindMerge {
				sum += g[j].Val
				j++
			}
			if j == len(g) {
				// No base record inside the stripe.
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: KindMerge, Val: sum})
				folded += j - 1
			} else {
				b := g[j] // first non-merge record in the stripe
				if b.Kind == KindPut {
					sum += b.Val
				}
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: KindPut, Val: sum})
				folded += j // j-1 absorbed merges plus base b
				shadowed += len(g) - j - 1
			}
		}

		// Finishing pass: drop trailing tombs invisible to every surviving
		// view, then anchor a trailing merge without a deeper base.
		_, inDeeper := c.deeper[k]
		for len(produced) > 0 {
			oldest := produced[len(produced)-1]
			if oldest.Kind != KindDelete || inDeeper {
				break
			}
			produced = produced[:len(produced)-1]
			tombs++
		}
		if len(produced) > 0 {
			oldest := &produced[len(produced)-1]
			if oldest.Kind == KindMerge && !inDeeper {
				oldest.Kind = KindPut
				mergeToPut++
			}
		}

		out = append(out, produced...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	c.records = out

	return Stats{
		In:          in,
		Out:         len(out),
		Shadowed:    shadowed,
		Folded:      folded,
		TombDropped: tombs,
		MergeToPut:  mergeToPut,
	}
}
