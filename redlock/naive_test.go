package redlock

// naiveEngine is a deliberately literal, step-by-step transcription of
// the specification. It shares no code with Engine so the randomized
// test can cross-check the optimized/concurrent implementation against
// an obviously correct reference.

import (
	"fmt"
)

type naiveRecord struct {
	token  int64
	expiry int64
}

type naiveEngine struct {
	n         int
	tn        int64
	d         int64
	maxTTL    int64
	recs      []map[string]naiveRecord
	quiet     []int64
	clock     int64
	nextToken int64
}

func newNaive(n int, tn, d, maxTTL int64) *naiveEngine {
	nv := &naiveEngine{
		n:         n,
		tn:        tn,
		d:         d,
		maxTTL:    maxTTL,
		recs:      make([]map[string]naiveRecord, n),
		quiet:     make([]int64, n),
		nextToken: 1,
	}
	for i := range nv.recs {
		nv.recs[i] = make(map[string]naiveRecord)
	}
	return nv
}

func (nv *naiveEngine) acquire(res string, ttl, start int64, rtt []int64) (AcquireResult, error) {
	// Step 1: argument validation.
	if res == "" {
		return AcquireResult{}, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if ttl < 1 || ttl > nv.maxTTL {
		return AcquireResult{}, fmt.Errorf("%w: ttl out of range", ErrInvalidArgument)
	}
	if len(rtt) != nv.n {
		return AcquireResult{}, fmt.Errorf("%w: bad rtt length", ErrInvalidArgument)
	}
	for _, r := range rtt {
		if r == -1 {
			continue
		}
		if r < 0 || r > 2*nv.tn {
			return AcquireResult{}, fmt.Errorf("%w: bad rtt value", ErrInvalidArgument)
		}
	}
	// Step 2: time validation.
	if start < 0 || start > maxTime {
		return AcquireResult{}, fmt.Errorf("%w: bad start", ErrInvalidTime)
	}
	// Step 3: clock regression.
	if start < nv.clock {
		return AcquireResult{}, fmt.Errorf("%w: start < T", ErrClockRewind)
	}

	// Accepted calls always take the next token.
	k := nv.nextToken
	nv.nextToken++

	c := start
	g := 0
	for i := 0; i < nv.n; i++ {
		r := rtt[i]
		if r == -1 {
			// Unreachable: node not executed, client clock pays Tn.
			c += nv.tn
			continue
		}
		a := c + r/2
		granted := false
		if a >= nv.quiet[i] {
			rec, ok := nv.recs[i][res]
			if !ok || rec.expiry <= a {
				nv.recs[i][res] = naiveRecord{token: k, expiry: a + ttl}
				granted = true
			}
		}
		if r <= nv.tn {
			c += r
			if granted {
				g++
			}
		} else {
			c += nv.tn
		}
	}
	cend := c
	dr := ttl*nv.d/1000 + 2
	v := ttl - (cend - start) - dr
	until := start + ttl - dr
	success := g >= nv.n/2+1 && v > 0
	if !success {
		for i := 0; i < nv.n; i++ {
			if rec, ok := nv.recs[i][res]; ok && rec.token == k {
				delete(nv.recs[i], res)
			}
		}
	}
	nv.clock = cend
	return AcquireResult{Token: k, Success: success, Grants: g, Validity: v, Until: until, Cend: cend}, nil
}

func (nv *naiveEngine) unlock(res string, k, now int64) (int, error) {
	if res == "" {
		return 0, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if k < 1 {
		return 0, fmt.Errorf("%w: bad token", ErrInvalidArgument)
	}
	if now < 0 || now > maxTime {
		return 0, fmt.Errorf("%w: bad now", ErrInvalidTime)
	}
	if now < nv.clock {
		return 0, fmt.Errorf("%w: now < T", ErrClockRewind)
	}
	removed := 0
	for i := 0; i < nv.n; i++ {
		if rec, ok := nv.recs[i][res]; ok && rec.token == k {
			delete(nv.recs[i], res)
			removed++
		}
	}
	nv.clock = now
	return removed, nil
}

func (nv *naiveEngine) restart(i int, now int64) error {
	if i < 0 || i >= nv.n {
		return fmt.Errorf("%w: bad node", ErrInvalidArgument)
	}
	if now < 0 || now > maxTime {
		return fmt.Errorf("%w: bad now", ErrInvalidTime)
	}
	if now < nv.clock {
		return fmt.Errorf("%w: now < T", ErrClockRewind)
	}
	nv.recs[i] = make(map[string]naiveRecord)
	nv.quiet[i] = now + nv.maxTTL
	nv.clock = now
	return nil
}

func (nv *naiveEngine) count(res string, k, now int64) (int, error) {
	if res == "" {
		return 0, fmt.Errorf("%w: empty resource", ErrInvalidArgument)
	}
	if k < 1 {
		return 0, fmt.Errorf("%w: bad token", ErrInvalidArgument)
	}
	if now < 0 || now > maxTime {
		return 0, fmt.Errorf("%w: bad now", ErrInvalidTime)
	}
	if now < nv.clock {
		return 0, fmt.Errorf("%w: now < T", ErrClockRewind)
	}
	count := 0
	for i := 0; i < nv.n; i++ {
		if rec, ok := nv.recs[i][res]; ok && rec.token == k && rec.expiry > now {
			count++
		}
	}
	return count, nil
}
