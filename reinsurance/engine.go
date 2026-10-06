package reinsurance

import (
	"sort"
	"sync"
)

type Engine struct {
	mu      sync.RWMutex
	terms   Terms
	totalXL int64

	policies map[string]*Policy
	claims   map[string]*Claim

	occurrences map[string]*occurrence
	order       []*occurrence
}

func NewEngine(t Terms) (*Engine, error) {
	if err := validateTerms(t); err != nil {
		return nil, err
	}
	return &Engine{
		terms:       t,
		totalXL:     t.XLLimit * int64(t.XLReinstatements+1),
		policies:    make(map[string]*Policy),
		claims:      make(map[string]*Claim),
		occurrences: make(map[string]*occurrence),
	}, nil
}

func (e *Engine) RegisterPolicy(p Policy) error {
	if p.ID == "" || p.Limit <= 0 || p.StartDay < 0 || p.EndDay < 0 || p.StartDay >= p.EndDay {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.policies[p.ID]; dup {
		return ErrPolicyDuplicate
	}
	if err := registerPolicy(&p, e.terms); err != nil {
		return err
	}
	e.policies[p.ID] = &p
	return nil
}

func (e *Engine) FileClaim(c Claim) (Claim, error) {
	if c.ID == "" || c.PolicyID == "" || c.Occurrence == "" || c.Amount <= 0 || c.TimeSec < 0 {
		return Claim{}, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	pol := e.policies[c.PolicyID]
	if pol == nil {
		return Claim{}, ErrPolicyNotFound
	}
	if _, dup := e.claims[c.ID]; dup {
		return Claim{}, ErrClaimExists
	}
	if c.Amount > pol.Limit {
		return Claim{}, ErrInvalidArgument
	}
	if c.TimeSec < pol.StartDay*86400 || c.TimeSec >= pol.EndDay*86400 {
		return Claim{}, ErrNotCovered
	}
	if occ, ok := e.occurrences[c.Occurrence]; ok && occ.timeSec != c.TimeSec {
		return Claim{}, ErrInvalidArgument
	}
	quota, surplus, net := splitClaim(pol, c.Amount)
	stored := &Claim{
		ID:          c.ID,
		PolicyID:    c.PolicyID,
		Occurrence:  c.Occurrence,
		TimeSec:     c.TimeSec,
		Amount:      c.Amount,
		QuotaPart:   quota,
		SurplusPart: surplus,
		NetPart:     net,
	}
	e.claims[c.ID] = stored
	idx := e.upsertOccurrence(stored.Occurrence, stored.TimeSec, net)
	e.recomputeFrom(idx)
	return *stored, nil
}

func (e *Engine) CancelClaim(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cl, ok := e.claims[id]
	if !ok {
		return ErrClaimNotFound
	}
	idx := sort.Search(len(e.order), func(i int) bool {
		return e.order[i].timeSec >= cl.TimeSec
	})
	delete(e.claims, id)
	occ := e.occurrences[cl.Occurrence]
	occ.net -= cl.NetPart
	if occ.net <= 0 {
		delete(e.occurrences, occ.id)
		e.removeOrder(idx, occ.id)
	}
	e.recomputeFrom(idx)
	return nil
}

func (e *Engine) Snapshot() ([]Policy, []OccurrenceResult, Totals) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pols := make([]Policy, 0, len(e.policies))
	for _, p := range e.policies {
		pols = append(pols, *p)
	}
	sort.Slice(pols, func(i, j int) bool { return pols[i].ID < pols[j].ID })

	occs := make([]OccurrenceResult, 0, len(e.order))
	var total Totals
	for _, o := range e.order {
		occs = append(occs, OccurrenceResult{
			Occurrence:   o.id,
			TimeSec:      o.timeSec,
			NetAggregate: o.net,
			XLRecovery:   o.xl,
		})
		total.XL += o.xl
	}
	for _, c := range e.claims {
		total.Claims++
		total.Gross += c.Amount
		total.Quota += c.QuotaPart
		total.Surplus += c.SurplusPart
		total.Net += c.NetPart
	}
	total.NetAfterXL = total.Net - total.XL
	total.XLRemaining = e.totalXL - total.XL
	return pols, occs, total
}

func (e *Engine) Claim(id string) (Claim, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.claims[id]
	if !ok {
		return Claim{}, false
	}
	return *c, true
}

func (e *Engine) upsertOccurrence(id string, timeSec, net int64) int {
	if occ, ok := e.occurrences[id]; ok {
		occ.net += net
		idx := e.firstIndexAtOrAfter(occ.timeSec, 0)
		for i := idx; i < len(e.order); i++ {
			if e.order[i].id == id {
				return i
			}
		}
		panic("reinsurance: occurrence missing from order")
	}
	occ := &occurrence{id: id, timeSec: timeSec, net: net}
	e.occurrences[id] = occ
	idx := e.firstIndexAtOrAfter(timeSec, 0)
	for idx < len(e.order) && e.order[idx].timeSec == timeSec && e.order[idx].id < id {
		idx++
	}
	e.order = append(e.order, nil)
	copy(e.order[idx+1:], e.order[idx:])
	e.order[idx] = occ
	return idx
}

func (e *Engine) removeOrder(hint int, id string) {
	for i := hint; i < len(e.order); i++ {
		if e.order[i].id == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			return
		}
	}
}

func (e *Engine) firstIndexAtOrAfter(timeSec int64, hint int) int {
	if hint < 0 {
		hint = 0
	}
	if hint > len(e.order) {
		hint = len(e.order)
	}
	idx := sort.Search(len(e.order)-hint, func(i int) bool {
		return e.order[hint+i].timeSec >= timeSec
	})
	return hint + idx
}

func (e *Engine) recomputeFrom(start int) {
	var used int64
	for i := 0; i < start; i++ {
		used += e.order[i].xl
	}
	for i := start; i < len(e.order); i++ {
		o := e.order[i]
		recovery := int64(0)
		if o.net > e.terms.XLDeductible && used < e.totalXL {
			need := o.net - e.terms.XLDeductible
			if need > e.terms.XLLimit {
				need = e.terms.XLLimit
			}
			remaining := e.totalXL - used
			if need > remaining {
				need = remaining
			}
			recovery = need
		}
		o.xl = recovery
		used += recovery
	}
}
