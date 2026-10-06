package reinsurance

import "sort"

// NaiveModel independently rebuilds every result from the full claim set:
// it sorts occurrences by (time, id) once and then consumes XL capacity in a
// single pass. It never reads engine internals, so it serves as an oracle.
type NaiveModel struct {
	terms Terms
}

type NaiveClaim struct {
	ID         string
	PolicyID   string
	Occurrence string
	TimeSec    int64
	Amount     int64
}

type NaiveResult struct {
	OccurrenceXL  map[string]int64
	OccurrenceNet map[string]int64
	Order         []OccurrenceResult
	ClaimSplit    map[string]Claim
	Totals        Totals
}

func NewNaiveModel(t Terms) (*NaiveModel, error) {
	if err := validateTerms(t); err != nil {
		return nil, err
	}
	return &NaiveModel{terms: t}, nil
}

func (m *NaiveModel) Compute(policies map[string]Policy, claims []NaiveClaim) NaiveResult {
	type occAgg struct {
		id      string
		timeSec int64
		net     int64
	}
	aggs := make(map[string]*occAgg)
	splits := make(map[string]Claim, len(claims))
	res := NaiveResult{OccurrenceXL: map[string]int64{}, OccurrenceNet: map[string]int64{}}

	for _, c := range claims {
		pol := policies[c.PolicyID]
		quota, surplus, net := splitClaim(&pol, c.Amount)
		splits[c.ID] = Claim{
			ID: c.ID, PolicyID: c.PolicyID, Occurrence: c.Occurrence,
			TimeSec: c.TimeSec, Amount: c.Amount,
			QuotaPart: quota, SurplusPart: surplus, NetPart: net,
		}
		a := aggs[c.Occurrence]
		if a == nil {
			a = &occAgg{id: c.Occurrence, timeSec: c.TimeSec}
			aggs[c.Occurrence] = a
		}
		a.net += net
		res.Totals.Claims++
		res.Totals.Gross += c.Amount
		res.Totals.Quota += quota
		res.Totals.Surplus += surplus
		res.Totals.Net += net
	}

	ordered := make([]*occAgg, 0, len(aggs))
	for _, a := range aggs {
		ordered = append(ordered, a)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].timeSec != ordered[j].timeSec {
			return ordered[i].timeSec < ordered[j].timeSec
		}
		return ordered[i].id < ordered[j].id
	})

	totalXL := m.terms.XLLimit * int64(m.terms.XLReinstatements+1)
	var used int64
	res.Order = make([]OccurrenceResult, 0, len(ordered))
	for _, a := range ordered {
		recovery := int64(0)
		if a.net > m.terms.XLDeductible && used < totalXL {
			recovery = a.net - m.terms.XLDeductible
			if recovery > m.terms.XLLimit {
				recovery = m.terms.XLLimit
			}
			if left := totalXL - used; recovery > left {
				recovery = left
			}
		}
		used += recovery
		res.OccurrenceXL[a.id] = recovery
		res.OccurrenceNet[a.id] = a.net
		res.Order = append(res.Order, OccurrenceResult{
			Occurrence: a.id, TimeSec: a.timeSec,
			NetAggregate: a.net, XLRecovery: recovery,
		})
		res.Totals.XL += recovery
	}
	res.Totals.NetAfterXL = res.Totals.Net - res.Totals.XL
	res.Totals.XLRemaining = totalXL - res.Totals.XL
	res.ClaimSplit = splits
	return res
}

func naiveCompute(t Terms, policies map[string]*Policy, claims map[string]*Claim) (map[string]int64, Totals) {
	m, _ := NewNaiveModel(t)
	pols := make(map[string]Policy, len(policies))
	for id, p := range policies {
		pols[id] = *p
	}
	cs := make([]NaiveClaim, 0, len(claims))
	for _, c := range claims {
		cs = append(cs, NaiveClaim{c.ID, c.PolicyID, c.Occurrence, c.TimeSec, c.Amount})
	}
	r := m.Compute(pols, cs)
	return r.OccurrenceXL, r.Totals
}
