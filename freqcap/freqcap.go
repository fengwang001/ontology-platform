// Package freqcap implements a per-user ad frequency controller with a
// global sliding-window cap, a per-campaign daily cap tightened by global
// pressure, and a creative repeat interval that grows with the run length
// of consecutive identical creatives.
//
// The three checks are evaluated in a fixed order so that every rejection
// reason is deterministic and reproducible:
//
//  1. Creative repeat interval: if the user's most recent admitted
//     exposure used the same creative, and s is the number of trailing
//     admitted exposures with that creative (regardless of whether they
//     fell out of the global window), then now - t_last >= g0*min(s,3)
//     must hold. A different most-recent creative imposes no interval.
//  2. Global window: with cg = #{admitted exposures with t+Wg > now},
//     require cg < Cg.
//  3. Campaign daily cap: with day = floor(now/86400) and n_c the number
//     of admitted exposures for the campaign on that day, the effective
//     cap is E = max(1, Cc - floor(cg/K)) using the cg from step 2, and
//     n_c < E must hold.
//
// Rejections are reported in the order: invalid parameter, clock
// rollback, creative interval, global window, campaign daily cap.
// Rejected operations never mutate any state.
//
// All methods are safe for concurrent use; operations on different users
// never block each other.
package freqcap

import (
	"fmt"
	"maps"
	"sort"
	"sync"
)

// MaxNow is the largest accepted timestamp (inclusive).
const MaxNow = int64(1_000_000_000_000_000)

const secondsPerDay = int64(86_400)

// RejectReason identifies why an operation was rejected.
type RejectReason int

const (
	// RejectInvalidParam: constructor parameter out of range, empty
	// user/campaign/creative, now out of [0, MaxNow], or bad batch length.
	RejectInvalidParam RejectReason = iota
	// RejectClockRollback: now is earlier than the user's last admitted
	// exposure timestamp.
	RejectClockRollback
	// RejectCreativeInterval: the same creative repeats too soon.
	RejectCreativeInterval
	// RejectGlobalWindow: the global sliding window is full.
	RejectGlobalWindow
	// RejectCampaignDaily: the campaign daily cap (tightened) is reached.
	RejectCampaignDaily
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidParam:
		return "invalid_param"
	case RejectClockRollback:
		return "clock_rollback"
	case RejectCreativeInterval:
		return "creative_interval"
	case RejectGlobalWindow:
		return "global_window"
	case RejectCampaignDaily:
		return "campaign_daily"
	}
	return "unknown"
}

// Decision is the outcome of Admit or Peek.
type Decision struct {
	Allowed bool
	// Reason is meaningful only when Allowed is false.
	Reason RejectReason
}

func (d Decision) String() string {
	if d.Allowed {
		return "admit"
	}
	return "reject(" + d.Reason.String() + ")"
}

// Request is a single item of AdmitBatch.
type Request struct {
	Camp string
	Cre  string
	Now  int64
}

// BatchResult is the outcome of AdmitBatch.
type BatchResult struct {
	// AdmittedAll is true when every request passed and the batch was
	// recorded atomically.
	AdmittedAll bool
	// FailedIndex is the index of the first rejected request; it is -1
	// when the batch shape itself is invalid (bad length or empty user).
	FailedIndex int
	// Reason is meaningful only when AdmittedAll is false.
	Reason RejectReason
}

// Stats exposes internal per-user counters for verification.
type Stats struct {
	Admitted int64 // total admitted exposures
	Evicted  int64 // total expired records popped from the window
	Windowed int   // records currently retained for the window
}

type exposure struct {
	t    int64
	camp string
	cre  string
}

type userState struct {
	recs     []exposure // admitted records with t+Wg > lastT, t non-decreasing
	hasLast  bool
	lastT    int64
	lastCre  string
	streak   int // trailing count of admitted exposures with creative lastCre
	day      int64
	counts   map[string]int // campaign -> admitted count on day
	admitted int64
	evicted  int64
}

type userEntry struct {
	mu sync.Mutex
	st userState
}

// Controller is a per-user frequency controller. The zero value is not
// usable; construct with NewController.
type Controller struct {
	wg int64 // global window length
	cg int64 // global window cap
	k  int64 // tightening step
	cc int64 // campaign daily cap
	g0 int64 // creative base interval

	users sync.Map // user string -> *userEntry
}

// NewController validates the constructor parameters and returns a
// Controller. Ranges: Wg in [1,1e9], Cg in [1,1e6], K in [1,1e6],
// Cc in [1,1e6], g0 in [1,1e9].
func NewController(wg, cg, k, cc, g0 int64) (*Controller, error) {
	if wg < 1 || wg > 1_000_000_000 {
		return nil, fmt.Errorf("freqcap: Wg=%d out of range [1,1e9]", wg)
	}
	if cg < 1 || cg > 1_000_000 {
		return nil, fmt.Errorf("freqcap: Cg=%d out of range [1,1e6]", cg)
	}
	if k < 1 || k > 1_000_000 {
		return nil, fmt.Errorf("freqcap: K=%d out of range [1,1e6]", k)
	}
	if cc < 1 || cc > 1_000_000 {
		return nil, fmt.Errorf("freqcap: Cc=%d out of range [1,1e6]", cc)
	}
	if g0 < 1 || g0 > 1_000_000_000 {
		return nil, fmt.Errorf("freqcap: g0=%d out of range [1,1e9]", g0)
	}
	return &Controller{wg: wg, cg: cg, k: k, cc: cc, g0: g0}, nil
}

func validCall(user, camp, cre string, now int64) bool {
	return user != "" && camp != "" && cre != "" && now >= 0 && now <= MaxNow
}

func (c *Controller) entry(user string) *userEntry {
	if v, ok := c.users.Load(user); ok {
		return v.(*userEntry)
	}
	e := &userEntry{}
	actual, _ := c.users.LoadOrStore(user, e)
	return actual.(*userEntry)
}

// windowCount returns cg: the number of retained records with t+Wg > now.
// Records are sorted by t, so a binary search locates the first live one.
func (c *Controller) windowCount(st *userState, now int64) int64 {
	idx := sort.Search(len(st.recs), func(i int) bool {
		return st.recs[i].t+c.wg > now
	})
	return int64(len(st.recs) - idx)
}

// decide runs the three checks in order. The caller must hold the user
// lock and must have validated the call parameters already.
func (c *Controller) decide(st *userState, camp, cre string, now int64) Decision {
	if st.hasLast && now < st.lastT {
		return Decision{Allowed: false, Reason: RejectClockRollback}
	}
	if st.hasLast && st.lastCre == cre {
		s := st.streak
		if s > 3 {
			s = 3
		}
		if now-st.lastT < c.g0*int64(s) {
			return Decision{Allowed: false, Reason: RejectCreativeInterval}
		}
	}
	cg := c.windowCount(st, now)
	if cg >= c.cg {
		return Decision{Allowed: false, Reason: RejectGlobalWindow}
	}
	day := now / secondsPerDay
	nc := 0
	if st.counts != nil && day == st.day {
		nc = st.counts[camp]
	}
	eff := c.cc - cg/c.k
	if eff < 1 {
		eff = 1
	}
	if int64(nc) >= eff {
		return Decision{Allowed: false, Reason: RejectCampaignDaily}
	}
	return Decision{Allowed: true}
}

// apply records an admitted exposure. It does not evict expired records;
// callers run evict at the appropriate commit point.
func (c *Controller) apply(st *userState, camp, cre string, now int64) {
	st.recs = append(st.recs, exposure{t: now, camp: camp, cre: cre})
	if st.hasLast && st.lastCre == cre {
		st.streak++
	} else {
		st.streak = 1
		st.lastCre = cre
	}
	st.hasLast = true
	st.lastT = now
	day := now / secondsPerDay
	if st.counts == nil || day != st.day {
		st.day = day
		st.counts = make(map[string]int)
	}
	st.counts[camp]++
	st.admitted++
}

// evict pops records expired relative to the user's last admitted
// timestamp. Because every future operation has now >= lastT (clock
// rollback is rejected), records expired at lastT stay expired forever.
func (c *Controller) evict(st *userState) {
	n := 0
	for n < len(st.recs) && st.recs[n].t+c.wg <= st.lastT {
		n++
	}
	if n > 0 {
		st.recs = st.recs[n:]
		st.evicted += int64(n)
	}
}

// Admit evaluates the three checks for (user, camp, cre, now) and records
// the exposure iff all pass.
func (c *Controller) Admit(user, camp, cre string, now int64) Decision {
	if !validCall(user, camp, cre, now) {
		return Decision{Allowed: false, Reason: RejectInvalidParam}
	}
	e := c.entry(user)
	e.mu.Lock()
	defer e.mu.Unlock()
	d := c.decide(&e.st, camp, cre, now)
	if !d.Allowed {
		return d
	}
	c.apply(&e.st, camp, cre, now)
	c.evict(&e.st)
	return Decision{Allowed: true}
}

// Peek evaluates exactly the same checks as Admit but never records
// anything and never changes any state.
func (c *Controller) Peek(user, camp, cre string, now int64) Decision {
	if !validCall(user, camp, cre, now) {
		return Decision{Allowed: false, Reason: RejectInvalidParam}
	}
	e := c.entry(user)
	e.mu.Lock()
	defer e.mu.Unlock()
	return c.decide(&e.st, camp, cre, now)
}

// AdmitBatch evaluates the requests for one user in list order; each
// admitted request affects the streak, cg and n_c seen by the following
// ones. If any request is rejected the whole batch is rolled back and the
// first failing index and reason are returned; only a fully passing batch
// is recorded.
func (c *Controller) AdmitBatch(user string, reqs []Request) BatchResult {
	if user == "" || len(reqs) == 0 || len(reqs) > 1000 {
		return BatchResult{FailedIndex: -1, Reason: RejectInvalidParam}
	}
	e := c.entry(user)
	e.mu.Lock()
	defer e.mu.Unlock()
	st := &e.st

	savedLen := len(st.recs)
	savedHasLast, savedLastT := st.hasLast, st.lastT
	savedLastCre, savedStreak := st.lastCre, st.streak
	savedDay, savedCounts := st.day, st.counts
	savedAdmitted := st.admitted
	// Protect the original counts map from in-place mutation so rollback
	// is a plain field restore; tentative admits work on a clone.
	if st.counts != nil {
		st.counts = maps.Clone(st.counts)
	}

	rollback := func() {
		st.recs = st.recs[:savedLen]
		st.hasLast, st.lastT = savedHasLast, savedLastT
		st.lastCre, st.streak = savedLastCre, savedStreak
		st.day, st.counts = savedDay, savedCounts
		st.admitted = savedAdmitted
	}

	for i, r := range reqs {
		if r.Camp == "" || r.Cre == "" || r.Now < 0 || r.Now > MaxNow {
			rollback()
			return BatchResult{FailedIndex: i, Reason: RejectInvalidParam}
		}
		d := c.decide(st, r.Camp, r.Cre, r.Now)
		if !d.Allowed {
			rollback()
			return BatchResult{FailedIndex: i, Reason: d.Reason}
		}
		c.apply(st, r.Camp, r.Cre, r.Now)
	}
	// Commit point: recycle records expired at the final admitted time.
	c.evict(st)
	return BatchResult{AdmittedAll: true, FailedIndex: -1}
}

// Stats returns the verification counters for a user.
func (c *Controller) Stats(user string) Stats {
	e := c.entry(user)
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{
		Admitted: e.st.admitted,
		Evicted:  e.st.evicted,
		Windowed: len(e.st.recs),
	}
}
