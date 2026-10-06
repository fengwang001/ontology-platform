package lease

import (
	"math/rand"
	"strings"
	"testing"
)

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, leases: map[string]naiveLease{}, offers: map[string][]naiveOffer{}, current: map[string]uint64{}}
}

func (m *naiveModel) check(now int, leaseID string, offerID uint64) (naiveLease, *naiveOffer, error) {
	if now < m.now {
		return naiveLease{}, nil, Error{Code: ClockRollback, Reason: "clock"}
	}
	lease, ok := m.leases[leaseID]
	if !ok {
		return naiveLease{}, nil, Error{Code: LeaseNotFound, Reason: "missing"}
	}
	var offer *naiveOffer
	if id, exists := m.current[leaseID]; exists && id == offerID {
		for i := range m.offers[leaseID] {
			if m.offers[leaseID][i].id == offerID {
				offer = &m.offers[leaseID][i]
			}
		}
	}
	return lease, offer, nil
}

func (m *naiveModel) commit(now int, lease naiveLease, leaseID string, offer *naiveOffer, resolved bool) {
	m.leases[leaseID] = lease
	if offer != nil {
		for i := range m.offers[leaseID] {
			if m.offers[leaseID][i].id == offer.id {
				m.offers[leaseID][i] = *offer
			}
		}
	}
	if resolved {
		delete(m.current, leaseID)
	}
	m.now = now
}

func naiveStatus(lease naiveLease, offer *naiveOffer, cfg Config, now int) (string, string) {
	status := "active"
	if !lease.everOffer && now >= lease.end-cfg.MinOfferDaysBeforeEnd {
		status = "protected"
	}
	if now >= lease.end {
		status = "month_to_month"
	}
	offerStatus := ""
	if offer != nil {
		due := offer.due
		if offer.awaiting == "landlord" {
			due = offer.due2
		}
		if now > due {
			offerStatus = "expired"
		} else {
			offerStatus = "open"
			if now >= lease.end {
				status = "active"
			}
		}
	}
	if lease.terminateAt != 0 && now >= lease.terminateAt {
		status = "terminated"
	}
	return status, offerStatus
}

func (m *naiveModel) snapshot(now int, leaseID string) (naiveSnapshot, error) {
	lease, ok := m.leases[leaseID]
	if !ok {
		return naiveSnapshot{}, Error{Code: LeaseNotFound, Reason: "missing"}
	}
	var offer *naiveOffer
	if id, exists := m.current[leaseID]; exists {
		for i := range m.offers[leaseID] {
			if m.offers[leaseID][i].id == id {
				offer = &m.offers[leaseID][i]
			}
		}
	}
	status, offerStatus := naiveStatus(lease, offer, m.cfg, now)
	return naiveSnapshot{lease: lease, status: status, offer: offer, offerStatus: offerStatus}, nil
}

func (m *naiveModel) create(now int, input CreateLeaseInput) error {
	if now < 0 || input.ID == "" || input.StartDay < 0 || input.EndDay <= input.StartDay ||
		input.MonthlyRentCents <= 0 || input.LastAdjustmentAt > input.StartDay {
		return Error{Code: InvalidArgument, Reason: "bad lease"}
	}
	if now < m.now {
		return Error{Code: ClockRollback, Reason: "clock"}
	}
	if _, ok := m.leases[input.ID]; ok {
		return Error{Code: InvalidState, Reason: "exists"}
	}
	m.leases[input.ID] = naiveLease{start: input.StartDay, end: input.EndDay, rent: input.MonthlyRentCents, adjustedAt: input.LastAdjustmentAt}
	m.now = now
	return nil
}

func (m *naiveModel) issue(now int, input IssueOfferInput) error {
	if now < 0 || input.LeaseID == "" || input.RentCents <= 0 || input.NewEndDay <= 0 {
		return Error{Code: InvalidArgument, Reason: "bad offer"}
	}
	if now < m.now {
		return Error{Code: ClockRollback, Reason: "clock"}
	}
	lease, ok := m.leases[input.LeaseID]
	if !ok {
		return Error{Code: LeaseNotFound, Reason: "missing"}
	}
	if input.NewEndDay <= lease.end {
		return Error{Code: InvalidArgument, Reason: "bad end"}
	}
	daysBefore := lease.end - now
	if daysBefore < m.cfg.MinOfferDaysBeforeEnd || daysBefore > m.cfg.MaxOfferDaysBeforeEnd {
		return Error{Code: InvalidState, Reason: "window"}
	}
	if _, exists := m.current[input.LeaseID]; exists {
		return Error{Code: InvalidState, Reason: "pending"}
	}
	status, _ := naiveStatus(lease, nil, m.cfg, now)
	if status == "month_to_month" || status == "terminated" {
		return Error{Code: InvalidState, Reason: "state"}
	}
	years := now - lease.adjustedAt
	if years < 0 {
		years = 0
	}
	years /= 365
	rate := years * m.cfg.AnnualStepBasisPoints
	if rate > m.cfg.CapBasisPoints {
		rate = m.cfg.CapBasisPoints
	}
	limit := lease.rent
	if years > 0 {
		limit += int(int64(lease.rent) * int64(rate) / 10000)
	}
	if input.RentCents > limit {
		return Error{Code: RentAboveCap, Reason: "cap"}
	}
	m.nextID++
	m.offers[input.LeaseID] = append(m.offers[input.LeaseID], naiveOffer{
		id: m.nextID, rent: input.RentCents, newEnd: input.NewEndDay, issued: now,
		due: now + m.cfg.ResponseDays, awaiting: "tenant",
	})
	m.current[input.LeaseID] = m.nextID
	lease.everOffer = true
	m.commit(now, lease, input.LeaseID, nil, false)
	return nil
}

func (m *naiveModel) withdraw(now int, leaseID string, offerID uint64) error {
	if now < 0 || leaseID == "" || offerID == 0 {
		return Error{Code: InvalidArgument, Reason: "bad ids"}
	}
	lease, offer, err := m.check(now, leaseID, offerID)
	if err != nil {
		return err
	}
	if offer == nil || offer.awaiting != "tenant" || offer.due < now {
		return Error{Code: InvalidState, Reason: "withdraw"}
	}
	offer.resolved = true
	m.commit(now, lease, leaseID, offer, true)
	return nil
}

func (m *naiveModel) tenantRespond(now int, input RespondInput) error {
	if now < 0 || input.LeaseID == "" || input.OfferID == 0 {
		return Error{Code: InvalidArgument, Reason: "bad ids"}
	}
	if input.Decision != Accept && input.Decision != Reject && input.Decision != Counter {
		return Error{Code: InvalidArgument, Reason: "decision"}
	}
	if input.Decision == Counter && input.CounterRentCents <= 0 {
		return Error{Code: InvalidArgument, Reason: "counter rent"}
	}
	lease, offer, err := m.check(now, input.LeaseID, input.OfferID)
	if err != nil {
		return err
	}
	if offer == nil || offer.awaiting != "tenant" {
		return Error{Code: InvalidState, Reason: "await"}
	}
	if now > offer.due {
		return Error{Code: LateResponse, Reason: "late"}
	}
	resolved := false
	switch input.Decision {
	case Accept:
		lease.start, lease.end = lease.end, offer.newEnd
		if offer.rent != lease.rent {
			lease.adjustedAt = lease.start
		}
		lease.rent, lease.everOffer = offer.rent, false
		offer.accepted, offer.resolved, resolved = true, true, true
	case Reject:
		offer.resolved, resolved = true, true
	case Counter:
		low, high := lease.rent, offer.rent
		if low > high {
			low, high = high, low
		}
		if offer.counterUsed || input.CounterRentCents <= low || input.CounterRentCents >= high {
			return Error{Code: InvalidState, Reason: "counter"}
		}
		offer.counterUsed = true
		offer.counterRent, offer.counterAt = input.CounterRentCents, now
		offer.due2, offer.awaiting = now+m.cfg.ResponseDays, "landlord"
	}
	m.commit(now, lease, input.LeaseID, offer, resolved)
	return nil
}

func (m *naiveModel) landlordCounter(now int, input RespondInput) error {
	if now < 0 || input.LeaseID == "" || input.OfferID == 0 {
		return Error{Code: InvalidArgument, Reason: "bad ids"}
	}
	if input.Decision != Accept && input.Decision != Reject {
		return Error{Code: InvalidArgument, Reason: "decision"}
	}
	lease, offer, err := m.check(now, input.LeaseID, input.OfferID)
	if err != nil {
		return err
	}
	if offer == nil || !offer.counterUsed || offer.awaiting != "landlord" {
		return Error{Code: InvalidState, Reason: "await"}
	}
	if now > offer.due2 {
		return Error{Code: LateResponse, Reason: "late"}
	}
	if input.Decision == Accept {
		lease.start, lease.end = lease.end, offer.newEnd
		if offer.counterRent != lease.rent {
			lease.adjustedAt = lease.start
		}
		lease.rent, lease.everOffer = offer.counterRent, false
		offer.accepted = true
	}
	offer.resolved = true
	m.commit(now, lease, input.LeaseID, offer, true)
	return nil
}

func (m *naiveModel) notice(now int, leaseID string) error {
	if now < 0 || leaseID == "" {
		return Error{Code: InvalidArgument, Reason: "bad id"}
	}
	lease, _, err := m.check(now, leaseID, 0)
	if err != nil {
		return err
	}
	var offer *naiveOffer
	if id, ok := m.current[leaseID]; ok {
		for i := range m.offers[leaseID] {
			if m.offers[leaseID][i].id == id {
				offer = &m.offers[leaseID][i]
			}
		}
	}
	status, _ := naiveStatus(lease, offer, m.cfg, now)
	if status != "month_to_month" || lease.noticeAt != 0 {
		return Error{Code: InvalidState, Reason: "notice"}
	}
	lease.noticeAt, lease.terminateAt = now, now+m.cfg.TerminationNoticeDays
	m.commit(now, lease, leaseID, nil, false)
	return nil
}

type naiveLease struct {
	start, end, rent, adjustedAt int
	everOffer                    bool
	noticeAt, terminateAt        int
}

type naiveOffer struct {
	id                           uint64
	rent, newEnd, issued, due    int
	counterRent, counterAt, due2 int
	counterUsed                  bool
	awaiting                     string
	resolved, accepted           bool
}

type naiveModel struct {
	cfg     Config
	now     int
	nextID  uint64
	leases  map[string]naiveLease
	offers  map[string][]naiveOffer
	current map[string]uint64
}

type naiveSnapshot struct {
	lease       naiveLease
	status      string
	offer       *naiveOffer
	offerStatus string
}

func TestRandomReplayAgainstNaiveModel(t *testing.T) {
	cfg := testConfig()
	for seed := int64(1); seed <= 80; seed++ {
		t.Run("seed", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			production, err := NewService(cfg)
			if err != nil {
				t.Fatal(err)
			}
			reference := newNaiveModel(cfg)
			ids := []string{}

			for step := 0; step < 120; step++ {
				now := reference.now + rng.Intn(7) - 1
				if now < 0 {
					now = 0
				}
				if len(ids) == 0 {
					now = 0
				}
				kind := rng.Intn(8)
				leaseID := ""
				if len(ids) > 0 {
					leaseID = ids[rng.Intn(len(ids))]
				}
				var productionError, referenceError error
				description := ""

				if kind == 0 || len(ids) == 0 {
					description = "create"
					id := "L" + string(rune('a'+len(ids)))
					if len(ids) > 0 && rng.Intn(4) == 0 {
						id = ids[0]
					}
					input := CreateLeaseInput{
						ID:               id,
						StartDay:         0,
						EndDay:           20 + rng.Intn(350),
						MonthlyRentCents: 90000 + rng.Intn(30000),
						LastAdjustmentAt: 0,
					}
					_, productionError = production.CreateLease(now, input)
					referenceError = reference.create(now, input)
					if productionError == nil {
						ids = append(ids, id)
					}
				} else {
					lease := reference.leases[leaseID]
					snap, _ := reference.snapshot(now, leaseID)
					var offerID uint64
					if snap.offer != nil {
						offerID = snap.offer.id
					}
					switch kind {
					case 1:
						description = "issue"
						input := IssueOfferInput{
							LeaseID:   leaseID,
							RentCents: lease.rent - 20000 + rng.Intn(80001),
							NewEndDay: lease.end + 1 + rng.Intn(400),
						}
						_, productionError = production.IssueOffer(now, input)
						referenceError = reference.issue(now, input)
					case 2:
						description = "withdraw"
						_, productionError = production.WithdrawOffer(now, WithdrawInput{LeaseID: leaseID, OfferID: offerID})
						referenceError = reference.withdraw(now, leaseID, offerID)
					case 3, 4:
						description = "tenant_respond"
						decision := Decision(Accept)
						switch rng.Intn(3) {
						case 1:
							decision = Reject
						case 2:
							decision = Counter
						}
						counterRent := lease.rent
						if snap.offer != nil {
							low, high := lease.rent, snap.offer.rent
							if low > high {
								low, high = high, low
							}
							if high > low+2 {
								counterRent = low + 1 + rng.Intn(high-low-1)
							}
						}
						input := RespondInput{LeaseID: leaseID, OfferID: offerID, Decision: decision, CounterRentCents: counterRent}
						_, productionError = production.TenantRespond(now, input)
						referenceError = reference.tenantRespond(now, input)
					case 5:
						description = "landlord_counter"
						decision := Accept
						if rng.Intn(2) == 0 {
							decision = Reject
						}
						input := RespondInput{LeaseID: leaseID, OfferID: offerID, Decision: decision}
						_, productionError = production.LandlordRespondToCounter(now, input)
						referenceError = reference.landlordCounter(now, input)
					case 6, 7:
						description = "termination_notice"
						_, productionError = production.GiveTerminationNotice(now, NoticeInput{LeaseID: leaseID})
						referenceError = reference.notice(now, leaseID)
					}
				}

				reason := compareErrorCode(productionError, referenceError)
				t.Logf("seed=%d step=%d now=%d op=%s lease=%s output_prod=%v output_ref=%v basis=%s",
					seed, step, now, description, leaseID, productionError, referenceError, reason)
				if !strings.HasPrefix(reason, "match") {
					t.Fatalf("error mismatch at seed %d step %d", seed, step)
				}

				for _, id := range ids {
					productionSnap, productionLookup := production.Snapshot(now, id)
					referenceSnap, referenceLookup := reference.snapshot(now, id)
					if (productionLookup == nil) != (referenceLookup == nil) {
						t.Fatalf("lookup mismatch seed %d step %d", seed, step)
					}
					if productionSnap.Status != LeaseStatus(referenceSnap.status) ||
						productionSnap.OfferStatus != OfferStatus(referenceSnap.offerStatus) ||
						productionSnap.Lease.StartDay != referenceSnap.lease.start ||
						productionSnap.Lease.EndDay != referenceSnap.lease.end ||
						productionSnap.Lease.MonthlyRentCents != referenceSnap.lease.rent ||
						productionSnap.Lease.LastAdjustmentAt != referenceSnap.lease.adjustedAt ||
						productionSnap.Lease.EverReceivedOffer != referenceSnap.lease.everOffer ||
						productionSnap.Lease.TerminationNoticeAt != referenceSnap.lease.noticeAt ||
						productionSnap.Lease.TerminationEffectiveAt != referenceSnap.lease.terminateAt ||
						(productionSnap.CurrentOffer != nil) != (referenceSnap.offer != nil) {
						t.Fatalf("state mismatch seed %d step %d lease %s: %+v vs %+v",
							seed, step, id, productionSnap, referenceSnap)
					}
				}
			}
		})
	}
}

func compareErrorCode(productionError, referenceError error) string {
	if productionError == nil && referenceError == nil {
		return "match: both accepted"
	}
	if productionError == nil || referenceError == nil {
		return "mismatch: one accepted and one rejected"
	}
	productionCode := productionError.(Error).Code
	referenceCode := referenceError.(Error).Code
	if productionCode != referenceCode {
		return "mismatch: " + string(productionCode) + " != " + string(referenceCode)
	}
	return "match: first fixed-order error is " + string(productionCode)
}
