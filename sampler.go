// Package sampler is a tenant-aware, windowed log rate-limiting sampler.
//
// For every (tenant, key) fingerprint and every window of W milliseconds,
// the first N records (sev < 4) are kept, then one out of every M is kept;
// records with sev >= 4 are always kept and never consume quota. Dropped
// records are accounted exactly once via summaries (see package report).
//
// All methods are safe for concurrent use; the result is equivalent to some
// serial order. Replaying the same accepted input sequence reproduces the
// same keep decisions and summaries.
package sampler

import (
	"errors"
	"sync"

	"sampler/keytable"
	"sampler/report"
	"sampler/window"
)

const (
	maxN    = 1_000_000
	maxM    = 1_000_000
	maxW    = 1_000_000_000
	maxKt   = 100_000
	maxTmax = 10_000
	maxNow  = 1_000_000_000_000
	maxLen  = 64
)

var (
	// ErrParam: a constructor or Record/Flush argument is out of range.
	ErrParam = errors.New("sampler: invalid parameter")
	// ErrClock: now is smaller than the largest accepted now.
	ErrClock = errors.New("sampler: clock regression")
	// ErrTenantLimit: a new tenant arrived while at the tenant cap.
	ErrTenantLimit = keytable.ErrTenantLimit
)

// Sampler keeps per-(tenant, key) windowed sampling state.
type Sampler struct {
	mu     sync.Mutex
	n, m   uint64
	w      uint64
	table  *keytable.Table
	maxNow uint64
}

// New validates the construction parameters and builds a Sampler.
// Ranges: N in [0,1e6], M in [1,1e6], W in [1,1e9] ms, Kt in [1,1e5],
// Tmax in [1,1e4]. Out-of-range values fail with ErrParam.
func New(n, m, w, kt, tmax uint64) (*Sampler, error) {
	if n > maxN || m < 1 || m > maxM || w < 1 || w > maxW ||
		kt < 1 || kt > maxKt || tmax < 1 || tmax > maxTmax {
		return nil, ErrParam
	}
	return &Sampler{n: n, m: m, w: w, table: keytable.New(int(kt), int(tmax))}, nil
}

// Record applies one log record. It returns whether the record is kept and
// at most one summary (Rolled or Evicted) produced while handling it.
//
// Rejections are checked in the order: invalid parameter, clock regression,
// tenant limit; only the first is reported and rejected calls change no
// state (including access order and tenant registration).
func (s *Sampler) Record(now uint64, tenant, key string, sev int) (bool, []report.Summary, error) {
	if now > maxNow || tenant == "" || len(tenant) > maxLen ||
		key == "" || len(key) > maxLen || sev < 0 || sev > 5 {
		return false, nil, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return false, nil, ErrClock
	}
	cur := window.Of(now, s.w)
	entry, existed, evicted, err := s.table.Upsert(tenant, key, cur)
	if err != nil {
		return false, nil, err
	}
	s.maxNow = now
	var sums []report.Summary
	switch {
	case evicted != nil && evicted.Entry.Dropped > 0:
		sums = append(sums, report.Summary{
			Tenant: tenant, Key: evicted.Key, Window: evicted.Entry.Win,
			Dropped: evicted.Entry.Dropped, Reason: report.Evicted,
		})
	case existed && entry.Stale(cur):
		if entry.Dropped > 0 {
			sums = append(sums, report.Summary{
				Tenant: tenant, Key: key, Window: entry.Win,
				Dropped: entry.Dropped, Reason: report.Rolled,
			})
		}
		entry.Reset(cur)
	}
	return entry.Keep(sev, s.n, s.m), sums, nil
}

// Pending returns the entry's currently undelivered dropped count for
// (tenant, key) and whether the entry exists. It is read-only.
func (s *Sampler) Pending(tenant, key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.table.Peek(tenant, key)
	return entry.Dropped, ok
}

// Flush delivers a Closed summary for every live entry whose window is
// older than floor(now/W) and whose dropped count is positive, visiting
// entries in (tenant, key) byte order. A summary is committed (dropped
// cleared, entry kept) only when sink returns nil; on the first sink error
// Flush stops, leaving the failed and later entries untouched, and returns
// the number of delivered summaries together with the error.
//
// A nil sink is ErrParam; now below the largest accepted now is ErrClock
// (on success the clock advances to now). The sink is called synchronously
// while the sampler lock is held and must not call back into the sampler.
func (s *Sampler) Flush(now uint64, sink report.Sink) (int, error) {
	if sink == nil {
		return 0, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return 0, ErrClock
	}
	s.maxNow = now
	cur := window.Of(now, s.w)
	delivered := 0
	for _, ref := range s.table.Sorted() {
		e := ref.Entry
		if e.Win >= cur || e.Dropped == 0 {
			continue
		}
		err := sink(report.Summary{
			Tenant: ref.Tenant, Key: ref.Key, Window: e.Win,
			Dropped: e.Dropped, Reason: report.Closed,
		})
		if err != nil {
			return delivered, err
		}
		e.Dropped = 0
		delivered++
	}
	return delivered, nil
}
