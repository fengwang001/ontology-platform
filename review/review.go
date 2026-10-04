// Package review implements a prescription pre-review engine on top of a
// drug formulary and an ingredient interaction table.
package review

import (
	"errors"
	"sort"

	"ontology/formulary"
	"ontology/interact"
)

// Prescription item parameters.
const (
	maxItems = 20
	maxPer   = 20
	maxTimes = 12
	maxDay   = 1_000_000
)

// Interaction grades.
const (
	gradeNotice  = 1
	gradeCaution = 2
	gradeBan     = 3
)

// Prescription statuses.
const (
	StatusActive  = 1
	StatusPending = 2
	StatusVoided  = 3
)

// Item is one line of a prescription: drug, tablets per dose, doses per day,
// and the half-open intake interval [Start, End).
type Item struct {
	Drug   string
	Per    int
	PerDay int
	Start  int
	End    int
}

// Hint is one grade-1/grade-2 overlapping ingredient pair.
type Hint struct {
	IngA  []byte
	IngB  []byte
	Grade int
}

// Rx is a recorded prescription.
type Rx struct {
	ID        int
	Doctor    string
	Patient   string
	Items     []Item
	SubmitNow int
	Status    int
}

// SubmitError reports a rejected Submit and, when relevant, the smallest
// offending new-item index (-1 when the reason is not item-specific).
type SubmitError struct {
	Reason error
	Index  int
}

func (e *SubmitError) Error() string { return e.Reason.Error() }
func (e *SubmitError) Unwrap() error { return e.Reason }

// Reject reasons, in precedence order.
var (
	ErrInvalid     = errors.New("review: invalid argument")
	ErrClock       = errors.New("review: clock moved backwards")
	ErrNoEntity    = errors.New("review: doctor, patient or drug not found")
	ErrNoPrivilege = errors.New("review: doctor lacks prescription privilege")
	ErrAllergy     = errors.New("review: patient allergic to ingredient")
	ErrBan         = errors.New("review: contraindicated ingredient pair")
	ErrOverMax     = errors.New("review: daily ingredient dose exceeds maximum")
	ErrNoRx        = errors.New("review: prescription or person not found")
	ErrNoAuth      = errors.New("review: not authorized")
	ErrState       = errors.New("review: prescription status does not allow operation")
)

type storedItem struct {
	drug   string
	ing    []byte
	mg     int
	per    int
	perDay int
	start  int
	end    int
	rx     int
}

func (it storedItem) daily() int64 { return int64(it.mg) * int64(it.per) * int64(it.perDay) }

func overlap(s1, e1, s2, e2 int) bool { return s1 < e2 && s2 < e1 }

// Engine is the prescription review engine. All methods are safe for
// concurrent use; calls are serialized through a capacity-one channel used
// as a mutex, so outcomes are equivalent to some serial order.
type Engine struct {
	mu       chan struct{}
	T        int
	f        *formulary.Formulary
	tab      *interact.Table
	doctors  map[string]int
	pharmac  map[string]struct{}
	patients map[string]struct{}
	rxs      map[int]*Rx
	items    map[string][]storedItem // patient -> active and pending items
	pending  []*Rx                   // by increasing rx id
	maxNow   int
	nextRx   int
	frozen   bool

	// queries is a non-exported probe counting interaction-table lookups
	// during the most recent accepted Submit.
	queries int
}

// NewEngine creates an engine with review expiry time T (1..100 days).
func NewEngine(T int) *Engine {
	if T < 1 || T > 100 {
		panic("review: T must be in [1,100]")
	}
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return &Engine{
		mu:       mu,
		T:        T,
		f:        formulary.New(),
		tab:      interact.New(),
		doctors:  map[string]int{},
		pharmac:  map[string]struct{}{},
		patients: map[string]struct{}{},
		rxs:      map[int]*Rx{},
		items:    map[string][]storedItem{},
	}
}

// Formulary exposes the catalogue for configuration.
func (e *Engine) Formulary() *formulary.Formulary { return e.f }

// Table exposes the interaction table for configuration.
func (e *Engine) Table() *interact.Table { return e.tab }

// AddDoctor registers a prescribing doctor with level 1..3.
func (e *Engine) AddDoctor(id string, level int) error {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	if id == "" || level < 1 || level > 3 {
		return ErrInvalid
	}
	if _, ok := e.doctors[id]; ok {
		return errors.New("review: doctor already exists")
	}
	e.doctors[id] = level
	return nil
}

// AddPharmacist registers a reviewing pharmacist.
func (e *Engine) AddPharmacist(id string) error {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	if id == "" {
		return ErrInvalid
	}
	if _, ok := e.pharmac[id]; ok {
		return errors.New("review: pharmacist already exists")
	}
	e.pharmac[id] = struct{}{}
	return nil
}

// AddPatient registers a patient.
func (e *Engine) AddPatient(id string) error {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	if id == "" {
		return ErrInvalid
	}
	if _, ok := e.patients[id]; ok {
		return errors.New("review: patient already exists")
	}
	e.patients[id] = struct{}{}
	return nil
}

// RxRecord returns a copy of a recorded prescription.
func (e *Engine) RxRecord(id int) (Rx, bool) {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	r, ok := e.rxs[id]
	if !ok {
		return Rx{}, false
	}
	cp := *r
	cp.Items = append([]Item(nil), r.Items...)
	return cp, true
}

// interactionQueries exposes the non-exported probe.
func (e *Engine) interactionQueries() int { return e.queries }

// expire lands all pending prescriptions due at or before now, in
// (deadline, rx id) order.
func (e *Engine) expire(now int) {
	sort.Slice(e.pending, func(i, j int) bool {
		di, dj := e.pending[i].SubmitNow+e.T, e.pending[j].SubmitNow+e.T
		if di != dj {
			return di < dj
		}
		return e.pending[i].ID < e.pending[j].ID
	})
	kept := e.pending[:0]
	for _, r := range e.pending {
		if now >= r.SubmitNow+e.T {
			r.Status = StatusVoided
			e.removeItems(r)
		} else {
			kept = append(kept, r)
		}
	}
	e.pending = kept
}

func (e *Engine) removeItems(r *Rx) {
	list := e.items[r.Patient]
	out := list[:0]
	for _, it := range list {
		if it.rx != r.ID {
			out = append(out, it)
		}
	}
	e.items[r.Patient] = out
}

// begin validates the common prefix (parameters then clock), lands expiries
// and advances the clock. Invalid-parameter and clock-rollback rejections
// happen before expiry landing and do not advance the clock.
func (e *Engine) begin(now int, valid bool) bool {
	if !valid {
		return false
	}
	if now < e.maxNow || now < 0 || now > maxDay {
		return false
	}
	e.expire(now)
	e.maxNow = now
	return true
}

type doseEvent struct {
	pos      int
	delta    int64
	newDelta int
	newIdx   int
}

// checkMax verifies, per ingredient and per day, that the sum of daily
// doses of all covering new and reference items does not strictly exceed the
// cap. Equality passes. It reports the smallest new-item index covering the
// earliest violating day, matching a day-by-day reference model.
func (e *Engine) checkMax(newItems []storedItem, ref []storedItem) error {
	type ev struct {
		delta  int64
		newD   int
		newIdx int
	}
	ingSet := map[string]struct{}{}
	for _, it := range newItems {
		ingSet[string(it.ing)] = struct{}{}
	}
	ingredients := make([]string, 0, len(ingSet))
	for ing := range ingSet {
		ingredients = append(ingredients, ing)
	}
	sort.Strings(ingredients)
	type violation struct {
		day, ingOrder int
		cover         map[int]int
	}
	var best *violation
	for ingOrder, ing := range ingredients {
		ingViolationDay := 1<<31 - 1
		ingCover := map[int]int{}
		capVal, hasCap := e.f.Max([]byte(ing))
		if !hasCap {
			continue
		}
		starts := map[int][]ev{}
		ends := map[int][]ev{}
		coords := map[int]struct{}{}
		add := func(pos int, x ev) {
			coords[pos] = struct{}{}
			if x.delta > 0 {
				starts[pos] = append(starts[pos], x)
			} else {
				ends[pos] = append(ends[pos], x)
			}
		}
		for i, it := range newItems {
			if string(it.ing) != ing {
				continue
			}
			d := it.daily()
			add(it.start, ev{d, 1, i})
			add(it.end, ev{-d, -1, i})
		}
		for _, it := range ref {
			if string(it.ing) != ing {
				continue
			}
			d := it.daily()
			add(it.start, ev{d, 0, -1})
			add(it.end, ev{-d, 0, -1})
		}
		days := make([]int, 0, len(coords))
		for c := range coords {
			days = append(days, c)
		}
		sort.Ints(days)
		for pos := range starts {
			sort.Slice(starts[pos], func(i, j int) bool { return starts[pos][i].newIdx < starts[pos][j].newIdx })
		}
		for pos := range ends {
			sort.Slice(ends[pos], func(i, j int) bool { return ends[pos][i].newIdx < ends[pos][j].newIdx })
		}
		var sum int64
		newCover := 0
		coverCount := map[int]int{}
		applyAll := func(xs []ev) {
			for _, x := range xs {
				sum += x.delta
				if x.newD != 0 {
					newCover += x.newD
					coverCount[x.newIdx] += x.newD
				}
			}
		}
		for _, pos := range days {
			applyAll(ends[pos])
			applyAll(starts[pos])
			if newCover > 0 && sum > int64(capVal) && pos < ingViolationDay {
				ingViolationDay = pos
				ingCover = map[int]int{}
				for k, v := range coverCount {
					if v > 0 {
						ingCover[k] = v
					}
				}
			}
		}
		if ingViolationDay == 1<<31-1 {
			continue
		}
		cand := &violation{day: ingViolationDay, ingOrder: ingOrder, cover: ingCover}
		if best == nil || cand.day < best.day ||
			(cand.day == best.day && cand.ingOrder < best.ingOrder) {
			best = cand
		}
	}
	if best == nil {
		return nil
	}
	for i := range newItems {
		if best.cover[i] > 0 {
			return &SubmitError{Reason: ErrOverMax, Index: i}
		}
	}
	return &SubmitError{Reason: ErrOverMax, Index: -1}
}

// Submit applies one prescription all-or-nothing. On success it returns the
// new prescription id, its status and the sorted hint list.
func (e *Engine) Submit(now int, doctor, patient string, items []Item) (int, int, []Hint, error) {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()

	valid := now >= 0 && now <= maxDay && doctor != "" && patient != "" &&
		len(items) >= 1 && len(items) <= maxItems
	seenDrug := map[string]bool{}
	for _, it := range items {
		if it.Per < 1 || it.Per > maxPer || it.PerDay < 1 || it.PerDay > maxTimes ||
			it.Start < now || it.Start >= it.End || it.End > maxDay || it.Drug == "" {
			valid = false
		}
		if seenDrug[it.Drug] {
			valid = false
		}
		seenDrug[it.Drug] = true
	}
	if !e.begin(now, valid) {
		if !valid {
			return 0, 0, nil, &SubmitError{Reason: ErrInvalid, Index: -1}
		}
		return 0, 0, nil, &SubmitError{Reason: ErrClock, Index: -1}
	}

	dl, docOK := e.doctors[doctor]
	_, patOK := e.patients[patient]
	newItems := make([]storedItem, len(items))
	for i, it := range items {
		d, ok := e.f.Drug(it.Drug)
		if !ok {
			return 0, 0, nil, &SubmitError{Reason: ErrNoEntity, Index: i}
		}
		newItems[i] = storedItem{
			drug: it.Drug, ing: append([]byte(nil), d.Ing...), mg: d.Mg,
			per: it.Per, perDay: it.PerDay, start: it.Start, end: it.End,
		}
	}
	if !docOK || !patOK {
		return 0, 0, nil, &SubmitError{Reason: ErrNoEntity, Index: -1}
	}

	for i, it := range newItems {
		d, _ := e.f.Drug(it.drug)
		if d.Level > dl {
			return 0, 0, nil, &SubmitError{Reason: ErrNoPrivilege, Index: i}
		}
	}
	for i, it := range newItems {
		if e.tab.Allergic(patient, it.ing) {
			return 0, 0, nil, &SubmitError{Reason: ErrAllergy, Index: i}
		}
	}

	ref := e.items[patient]
	e.queries = 0
	gradeCache := map[[2]string]int{}
	grade := func(a, b []byte) int {
		if string(a) == string(b) {
			return 0
		}
		var lo, hi string
		if string(a) < string(b) {
			lo, hi = string(a), string(b)
		} else {
			lo, hi = string(b), string(a)
		}
		key := [2]string{lo, hi}
		if g, ok := gradeCache[key]; ok {
			return g
		}
		e.queries++
		g := e.tab.Grade(a, b)
		gradeCache[key] = g
		return g
	}

	for i := range newItems {
		for j := i + 1; j < len(newItems); j++ {
			a, b := newItems[i], newItems[j]
			if string(a.ing) != string(b.ing) && overlap(a.start, a.end, b.start, b.end) &&
				grade(a.ing, b.ing) == gradeBan {
				return 0, 0, nil, &SubmitError{Reason: ErrBan, Index: i}
			}
		}
		for _, r := range ref {
			if string(newItems[i].ing) != string(r.ing) &&
				overlap(newItems[i].start, newItems[i].end, r.start, r.end) &&
				grade(newItems[i].ing, r.ing) == gradeBan {
				return 0, 0, nil, &SubmitError{Reason: ErrBan, Index: i}
			}
		}
	}

	if err := e.checkMax(newItems, ref); err != nil {
		return 0, 0, nil, err
	}

	type pairKey struct{ a, b string }
	hintMap := map[pairKey]int{}
	addHint := func(a, b []byte) {
		g := grade(a, b)
		if g != gradeNotice && g != gradeCaution {
			return
		}
		lo, hi := string(a), string(b)
		if lo > hi {
			lo, hi = hi, lo
		}
		k := pairKey{lo, hi}
		if cur, ok := hintMap[k]; !ok || g > cur {
			hintMap[k] = g
		}
	}
	for i := range newItems {
		for j := i + 1; j < len(newItems); j++ {
			a, b := newItems[i], newItems[j]
			if string(a.ing) != string(b.ing) && overlap(a.start, a.end, b.start, b.end) {
				addHint(a.ing, b.ing)
			}
		}
		for _, r := range ref {
			if string(newItems[i].ing) != string(r.ing) &&
				overlap(newItems[i].start, newItems[i].end, r.start, r.end) {
				addHint(newItems[i].ing, r.ing)
			}
		}
	}
	hints := make([]Hint, 0, len(hintMap))
	for k, g := range hintMap {
		hints = append(hints, Hint{IngA: []byte(k.a), IngB: []byte(k.b), Grade: g})
	}
	sort.Slice(hints, func(i, j int) bool {
		if hints[i].Grade != hints[j].Grade {
			return hints[i].Grade > hints[j].Grade
		}
		if string(hints[i].IngA) != string(hints[j].IngA) {
			return string(hints[i].IngA) < string(hints[j].IngA)
		}
		return string(hints[i].IngB) < string(hints[j].IngB)
	})

	needsReview := false
	for _, h := range hints {
		if h.Grade == gradeCaution {
			needsReview = true
		}
	}

	if !e.frozen {
		e.f.Freeze()
		e.tab.Freeze()
		e.frozen = true
	}

	e.nextRx++
	id := e.nextRx
	rxItems := make([]Item, len(items))
	copy(rxItems, items)
	r := &Rx{
		ID:        id,
		Doctor:    doctor,
		Patient:   patient,
		Items:     rxItems,
		SubmitNow: now,
		Status:    StatusActive,
	}
	if needsReview {
		r.Status = StatusPending
	}
	e.rxs[id] = r
	stored := make([]storedItem, len(newItems))
	for i := range newItems {
		stored[i] = newItems[i]
		stored[i].rx = id
	}
	e.items[patient] = append(e.items[patient], stored...)
	if needsReview {
		e.pending = append(e.pending, r)
	}
	return id, r.Status, hints, nil
}

// Approve makes a pending prescription active without re-checking.
func (e *Engine) Approve(now int, pharmacist string, rx int) error {
	return e.review(now, pharmacist, rx, true)
}

// Deny voids a pending prescription and releases its occupied capacity
// without re-checking.
func (e *Engine) Deny(now int, pharmacist string, rx int) error {
	return e.review(now, pharmacist, rx, false)
}

func (e *Engine) review(now int, pharmacist string, rx int, approve bool) error {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	if !e.begin(now, now >= 0 && now <= maxDay && pharmacist != "" && rx >= 1) {
		if now < 0 || now > maxDay || pharmacist == "" || rx < 1 {
			return ErrInvalid
		}
		return ErrClock
	}
	r, rxOK := e.rxs[rx]
	_, phOK := e.pharmac[pharmacist]
	if !rxOK || !phOK {
		return ErrNoRx
	}
	if r.Status != StatusPending {
		return ErrState
	}
	if approve {
		r.Status = StatusActive
		idx := -1
		for i, p := range e.pending {
			if p.ID == rx {
				idx = i
				break
			}
		}
		if idx >= 0 {
			e.pending = append(e.pending[:idx], e.pending[idx+1:]...)
		}
	} else {
		r.Status = StatusVoided
		e.removeItems(r)
		idx := -1
		for i, p := range e.pending {
			if p.ID == rx {
				idx = i
				break
			}
		}
		if idx >= 0 {
			e.pending = append(e.pending[:idx], e.pending[idx+1:]...)
		}
	}
	return nil
}

// Stop truncates every item of an active or pending prescription to end at
// min(end, now). Items whose interval becomes empty stop occupying capacity
// and stop participating in interactions.
func (e *Engine) Stop(now int, doctor string, rx int) error {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()
	if !e.begin(now, now >= 0 && now <= maxDay && doctor != "" && rx >= 1) {
		if now < 0 || now > maxDay || doctor == "" || rx < 1 {
			return ErrInvalid
		}
		return ErrClock
	}
	r, rxOK := e.rxs[rx]
	dl, docOK := e.doctors[doctor]
	if !rxOK || !docOK {
		return ErrNoRx
	}
	if r.Doctor != doctor && dl < 3 {
		return ErrNoAuth
	}
	if r.Status != StatusActive && r.Status != StatusPending {
		return ErrState
	}
	list := e.items[r.Patient]
	out := list[:0]
	for _, it := range list {
		if it.rx != rx {
			out = append(out, it)
			continue
		}
		if now < it.end {
			it.end = now
		}
		if it.end > it.start {
			out = append(out, it)
		}
	}
	e.items[r.Patient] = out
	for i := range r.Items {
		if now < r.Items[i].End {
			r.Items[i].End = now
		}
	}
	return nil
}
