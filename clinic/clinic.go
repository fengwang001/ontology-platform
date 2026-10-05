// Package clinic provides the vaccination outpatient service: historical
// backfill (Record), same-day administration (Administer) with lot
// deduction, and read-only per-dose evaluation.
package clinic

import (
	"errors"
	"sort"
	"sync"

	"ontology/lotstock"
	"ontology/vaxrule"
)

// Service errors, declared in the documented rejection order.
var (
	ErrInvalidArg    = errors.New("clinic: invalid argument")
	ErrClockRollback = errors.New("clinic: clock moved backwards")
	ErrNotFound      = errors.New("clinic: patient or series not found")
	ErrNotGranted    = errors.New("clinic: nurse not granted")
	ErrDuplicate     = errors.New("clinic: duplicate record")
	ErrCompleted     = errors.New("clinic: series already completed")
	ErrNoStock       = errors.New("clinic: no usable stock")
	ErrExists        = errors.New("clinic: already exists")
)

// TooEarlyError reports that today's administration would be invalid.
type TooEarlyError struct {
	Reason   string
	Earliest int
}

func (e *TooEarlyError) Error() string {
	return "clinic: too early: " + e.Reason
}

type patient struct {
	birth   int
	records []vaxrule.Entry
	state   vaxrule.State
}

// Clinic is the stateful, concurrency-safe service. All operations are
// serialized under one mutex, so outcomes equal some serial ordering.
type Clinic struct {
	mu       sync.RWMutex
	books    *vaxrule.Books
	stock    *lotstock.Stock
	patients map[string]*patient
	nurses   map[string]bool
	lastNow  int
	hasNow   bool
	touched  int64
}

// New creates a clinic backed by fresh rule and stock stores.
func New() *Clinic {
	return &Clinic{
		books:    vaxrule.NewBooks(),
		stock:    lotstock.New(),
		patients: map[string]*patient{},
		nurses:   map[string]bool{},
	}
}

// AddSeries registers a vaccine series.
func (c *Clinic) AddSeries(name string, live bool, n int, minAge, minInt []int, r int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.books.Add(vaxrule.Series{Name: name, Live: live, N: n, MinAge: append([]int(nil), minAge...), MinInt: append([]int(nil), minInt...), R: r})
	if errors.Is(err, vaxrule.ErrSeriesExists) {
		return ErrExists
	}
	return err
}

// AddPatient registers a patient with a birth date in [0,1e6].
func (c *Clinic) AddPatient(p string, birth int) error {
	if p == "" || birth < 0 || birth > vaxrule.MaxDay {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.patients[p]; ok {
		return ErrExists
	}
	c.patients[p] = &patient{
		birth: birth,
		state: vaxrule.State{Sums: map[string]vaxrule.Summary{}},
	}
	return nil
}

// AddLot registers an inventory lot.
func (c *Clinic) AddLot(lot, series string, exp, qty int) error {
	if lot == "" || series == "" || exp < 0 || exp > vaxrule.MaxDay || qty < 0 || qty > vaxrule.MaxDay {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.books.Get(series); !ok {
		return ErrNotFound
	}
	err := c.stock.Add(lotstock.Lot{Lot: lot, Series: series, Exp: exp, Qty: qty})
	if errors.Is(err, lotstock.ErrLotExists) {
		return ErrExists
	}
	return err
}

// Quarantine isolates (on=true) or releases (on=false) a lot.
func (c *Clinic) Quarantine(lot string, on bool) error {
	if lot == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.stock.Quarantine(lot, on)
	if errors.Is(err, lotstock.ErrNoLot) {
		return ErrNotFound
	}
	return err
}

// Grant authorizes a nurse to administer vaccines. It is idempotent.
func (c *Clinic) Grant(user string) error {
	if user == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nurses[user] = true
	return nil
}

func (c *Clinic) checkClock(now int) bool {
	if now < 0 || now > vaxrule.MaxDay {
		return false
	}
	if c.hasNow && now < c.lastNow {
		return false
	}
	return true
}

func (c *Clinic) advance(now int) {
	if !c.hasNow || now > c.lastNow {
		c.lastNow = now
	}
	c.hasNow = true
}

// Record backfills a historical record at date d (birth <= d <= now).
// It never intercepts invalid records and never deducts stock; out-of
// order backfill is allowed and flips prior verdicts on recomputation.
func (c *Clinic) Record(now int, p, series string, d int) error {
	if now < 0 || now > vaxrule.MaxDay || p == "" || series == "" || d < 0 || d > vaxrule.MaxDay {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checkClock(now) {
		return ErrClockRollback
	}
	pat, ok := c.patients[p]
	if !ok {
		return ErrNotFound
	}
	if _, ok := c.books.Get(series); !ok {
		return ErrNotFound
	}
	if d < pat.birth || d > now {
		return ErrInvalidArg
	}
	for _, e := range pat.records {
		if e.Series == series && e.Date == d {
			return ErrDuplicate
		}
	}
	c.advance(now)
	pat.records = append(pat.records, vaxrule.Entry{Series: series, Date: d})
	st, _, err := vaxrule.Recompute(c.books, pat.birth, pat.records)
	if err != nil {
		panic("clinic: internal state referenced unknown series: " + err.Error())
	}
	pat.state = st
	return nil
}

// RecordVerdict is one judged record.
type RecordVerdict struct {
	Series string
	Date   int
	Valid  bool
	Extra  bool
	Dose   int
	Reason string
}

// NextDose is the earliest pending dose for one unfinished series.
type NextDose struct {
	Series string
	Dose   int
	Date   int
}

// Report is the read-only evaluation snapshot.
type Report struct {
	Records []RecordVerdict // ordered by (date, series)
	Next    []NextDose      // unfinished series, ordered by series name
}

// Evaluate returns per-record verdicts and each unfinished series'
// next dose number and earliest valid date at/after now.
func (c *Clinic) Evaluate(now int, p string) (Report, error) {
	if now < 0 || now > vaxrule.MaxDay || p == "" {
		return Report{}, ErrInvalidArg
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.checkClock(now) {
		return Report{}, ErrClockRollback
	}
	pat, ok := c.patients[p]
	if !ok {
		return Report{}, ErrNotFound
	}
	res, err := vaxrule.Evaluate(c.books, pat.birth, pat.records)
	if err != nil {
		return Report{}, err
	}
	rep := Report{}
	for _, idx := range res.Order {
		v := res.Verdicts[idx]
		e := pat.records[idx]
		rep.Records = append(rep.Records, RecordVerdict{
			Series: e.Series,
			Date:   e.Date,
			Valid:  v.Valid,
			Extra:  v.Extra,
			Dose:   v.Dose,
			Reason: v.Reason.String(),
		})
	}
	// Sorted summaries needed for earliest-date calculation: recompute
	// is pure and the per-series summaries match res exactly.
	st, _, err := vaxrule.Recompute(c.books, pat.birth, pat.records)
	if err != nil {
		return Report{}, err
	}
	names := c.books.Names()
	for _, name := range names {
		s, _ := c.books.Get(name)
		sum := st.Sums[name]
		if sum.ValidCount >= s.N {
			continue
		}
		date, _ := vaxrule.Earliest(s, pat.birth, now, sum, st.Lives)
		rep.Next = append(rep.Next, NextDose{
			Series: name,
			Dose:   sum.ValidCount + 1,
			Date:   date,
		})
	}
	return rep, nil
}

// Administer performs a same-day vaccination: it validates in the fixed
// precedence, deducts one usable lot only after every check passes, and
// appends a record that is guaranteed valid.
func (c *Clinic) Administer(now int, nurse, p, series string) (string, error) {
	if now < 0 || now > vaxrule.MaxDay || nurse == "" || p == "" || series == "" {
		return "", ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checkClock(now) {
		return "", ErrClockRollback
	}
	pat, ok := c.patients[p]
	if !ok {
		return "", ErrNotFound
	}
	s, ok := c.books.Get(series)
	if !ok {
		return "", ErrNotFound
	}
	if !c.nurses[nurse] {
		return "", ErrNotGranted
	}
	sum := pat.state.Sums[series]
	if sum.HasLast && sum.LastDate == now {
		return "", ErrDuplicate
	}
	if sum.ValidCount >= s.N {
		return "", ErrCompleted
	}
	// Administration reads only O(1) cached summaries; no raw record
	// rows are scanned, so touched does not grow with history size.
	earliest, reason := vaxrule.Earliest(s, pat.birth, now, sum, pat.state.Lives)
	if reason != vaxrule.ReasonNone {
		return "", &TooEarlyError{Reason: reason.String(), Earliest: earliest}
	}
	lot, err := c.stock.Pick(series, now)
	if err != nil {
		return "", ErrNoStock
	}
	c.advance(now)
	pat.records = append(pat.records, vaxrule.Entry{Series: series, Date: now})
	st, _, rerr := vaxrule.Recompute(c.books, pat.birth, pat.records)
	if rerr != nil {
		panic("clinic: internal state referenced unknown series: " + rerr.Error())
	}
	pat.state = st
	return lot, nil
}

// Touched reports raw vaccination-record rows read since the last reset.
// Administer never scans records (only cached summaries), so it stays
// constant regardless of patient history size.
func (c *Clinic) Touched() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.touched
}

// ResetTouched zeroes the raw-record-read counter.
func (c *Clinic) ResetTouched() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touched = 0
}

// Records returns a copy of a patient's records in (date, series) order.
func (c *Clinic) Records(p string) ([]vaxrule.Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pat, ok := c.patients[p]
	if !ok {
		return nil, false
	}
	out := append([]vaxrule.Entry(nil), pat.records...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Series < out[j].Series
	})
	return out, true
}

// LotQty reports the remaining quantity of a lot (0 if unknown).
func (c *Clinic) LotQty(lot string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stock.Qty(lot)
}
