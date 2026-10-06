package lease

import (
	"errors"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		MinOfferDaysBeforeEnd: 30,
		MaxOfferDaysBeforeEnd: 90,
		ResponseDays:          10,
		TerminationNoticeDays: 30,
		AnnualStepBasisPoints: 1000,
		CapBasisPoints:        5000,
	}
}

func newTestService(t testing.TB) *Service {
	t.Helper()
	service, err := NewService(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createTestLease(t testing.TB, service *Service, now int, input CreateLeaseInput) Snapshot {
	t.Helper()
	snapshot, err := service.CreateLease(now, input)
	if err != nil {
		t.Fatalf("create lease: %v", err)
	}
	return snapshot
}

func baseLease() CreateLeaseInput {
	return CreateLeaseInput{ID: "L1", StartDay: 0, EndDay: 400, MonthlyRentCents: 100000, LastAdjustmentAt: 0}
}

func errorCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	var serviceError Error
	if !errors.As(err, &serviceError) {
		t.Fatalf("expected lease.Error, got %T %v", err, err)
	}
	return serviceError.Code
}

func TestOfferWindowBoundaries(t *testing.T) {
	for _, day := range []int{310, 370} {
		service := newTestService(t)
		createTestLease(t, service, 0, baseLease())
		_, err := service.IssueOffer(day, IssueOfferInput{LeaseID: "L1", RentCents: 100000, NewEndDay: 800})
		if err != nil {
			t.Fatalf("day %d should be in window: %v", day, err)
		}
	}

	for _, day := range []int{309, 371} {
		service := newTestService(t)
		createTestLease(t, service, 0, baseLease())
		_, err := service.IssueOffer(day, IssueOfferInput{LeaseID: "L1", RentCents: 100000, NewEndDay: 800})
		if errorCode(t, err) != InvalidState {
			t.Fatalf("day %d error = %v, want invalid_state", day, err)
		}
	}
}

func TestIncreaseCapExactlyOneCentOverAndYearBoundary(t *testing.T) {
	tests := []struct {
		name    string
		issued  int
		rent    int
		wantErr ErrorCode
	}{
		{"before first year", 364, 100004, RentAboveCap},
		{"at first year exact floor", 365, 110003, ""},
		{"at first year one cent over", 365, 110004, RentAboveCap},
		{"four years exactly", 1460, 140004, ""},
		{"four years one cent over", 1460, 140005, RentAboveCap},
		{"five years capped exactly", 1825, 150004, ""},
		{"five years capped one cent over", 1825, 150005, RentAboveCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t)
			input := baseLease()
			input.MonthlyRentCents = 100003
			input.EndDay = tt.issued + 30
			input.LastAdjustmentAt = tt.issued - 365
			if tt.name == "before first year" {
				input.LastAdjustmentAt = tt.issued - 364
			}
			if strings.Contains(tt.name, "four years") {
				input.LastAdjustmentAt = tt.issued - 1460
			}
			if strings.Contains(tt.name, "five years") {
				input.LastAdjustmentAt = tt.issued - 1825
			}
			createTestLease(t, service, 0, input)
			_, err := service.IssueOffer(tt.issued, IssueOfferInput{LeaseID: "L1", RentCents: tt.rent, NewEndDay: 2600})
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && errorCode(t, err) != tt.wantErr {
				t.Fatalf("error = %v, want %s", err, tt.wantErr)
			}
		})
	}
}

func TestResponseDeadlineBoundary(t *testing.T) {
	service := newTestService(t)
	createTestLease(t, service, 0, baseLease())
	offer := mustIssue(t, service, 310, 100000)
	if _, err := service.TenantRespond(offer.ResponseDue, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Accept}); err != nil {
		t.Fatalf("response on due day should succeed: %v", err)
	}

	service = newTestService(t)
	createTestLease(t, service, 0, baseLease())
	offer = mustIssue(t, service, 365, 109999)
	_, err := service.TenantRespond(offer.ResponseDue+1, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Accept})
	if errorCode(t, err) != LateResponse {
		t.Fatalf("next day error = %v, want late_response", err)
	}
}

func TestCounterRentIntervalBoundariesAndSingleUse(t *testing.T) {
	tests := []struct {
		name string
		rent int
		want ErrorCode
	}{
		{"low endpoint current rent", 100000, InvalidState},
		{"strictly inside", 105000, ""},
		{"high endpoint offer rent", 110000, InvalidState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t)
			createTestLease(t, service, 0, baseLease())
			offer := mustIssue(t, service, 365, 110000)
			_, err := service.TenantRespond(365, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Counter, CounterRentCents: tt.rent})
			if tt.want == "" && err != nil {
				t.Fatalf("counter: %v", err)
			}
			if tt.want != "" && errorCode(t, err) != tt.want {
				t.Fatalf("counter error = %v, want %s", err, tt.want)
			}
		})
	}

	service := newTestService(t)
	createTestLease(t, service, 0, baseLease())
	offer := mustIssue(t, service, 365, 110000)
	if _, err := service.TenantRespond(365, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Counter, CounterRentCents: 105000}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LandlordRespondToCounter(366, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Reject}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TenantRespond(367, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Counter, CounterRentCents: 104000}); errorCode(t, err) != InvalidState {
		t.Fatalf("second counter error = %v, want invalid_state", err)
	}
}

func TestProtectionTriggerAndNoTrigger(t *testing.T) {
	service := newTestService(t)
	createTestLease(t, service, 0, baseLease())
	snapshot, err := service.Snapshot(370, "L1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusProtected {
		t.Fatalf("status = %s, want protected", snapshot.Status)
	}
	snapshot, err = service.Snapshot(400, "L1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusMonthToMonth {
		t.Fatalf("status = %s, want month_to_month", snapshot.Status)
	}

	service = newTestService(t)
	createTestLease(t, service, 0, baseLease())
	offer := mustIssue(t, service, 369, 100000)
	if _, err := service.WithdrawOffer(370, WithdrawInput{LeaseID: "L1", OfferID: offer.ID}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = service.Snapshot(370, "L1")
	if snapshot.Status != StatusActive {
		t.Fatalf("an earlier received offer prevents protection, got %s", snapshot.Status)
	}
}

func TestOpenOfferPastEndBeforeResponseDeadlineRemainsPending(t *testing.T) {
	cfg := testConfig()
	cfg.ResponseDays = 40
	service, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	input := baseLease()
	input.EndDay = 425
	if _, err := service.CreateLease(0, input); err != nil {
		t.Fatal(err)
	}
	offer := mustIssue(t, service, 395, 100000)
	snapshot, err := service.Snapshot(425, "L1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusActive || snapshot.OfferStatus != OfferOpen {
		t.Fatalf("status=%s offer=%s, want active/open before response deadline", snapshot.Status, snapshot.OfferStatus)
	}
	snapshot, err = service.TenantRespond(offer.ResponseDue, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Accept})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Lease.StartDay != 425 || snapshot.Status != StatusActive {
		t.Fatalf("accepted renewal after old end: %+v", snapshot)
	}
}

func TestSameRentRenewalDoesNotResetAdjustment(t *testing.T) {
	service := newTestService(t)
	createTestLease(t, service, 0, baseLease())
	offer := mustIssue(t, service, 365, 100000)
	snapshot, err := service.TenantRespond(365, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Accept})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Lease.LastAdjustmentAt != 0 || snapshot.Lease.StartDay != 400 {
		t.Fatalf("renewal = %+v, wanted unchanged adjustment and contiguous start", snapshot.Lease)
	}
	if snapshot.Status != StatusActive {
		t.Fatalf("status = %s, want active", snapshot.Status)
	}
}

func TestErrorOrderAndRejectedOperationLeavesNoTrace(t *testing.T) {
	service := newTestService(t)
	createTestLease(t, service, 10, baseLease())
	offer := mustIssue(t, service, 310, 100000)
	before, _ := service.Snapshot(9, "L1")

	_, err := service.IssueOffer(9, IssueOfferInput{LeaseID: "", RentCents: 1, NewEndDay: 0})
	if errorCode(t, err) != InvalidArgument {
		t.Fatalf("invalid argument before clock rollback, got %v", err)
	}
	_, err = service.IssueOffer(9, IssueOfferInput{LeaseID: "missing", RentCents: 1, NewEndDay: 1})
	if errorCode(t, err) != ClockRollback {
		t.Fatalf("clock before missing lease, got %v", err)
	}
	_, err = service.IssueOffer(311, IssueOfferInput{LeaseID: "missing", RentCents: 1, NewEndDay: 1})
	if errorCode(t, err) != LeaseNotFound {
		t.Fatalf("missing lease before state, got %v", err)
	}
	_, err = service.IssueOffer(312, IssueOfferInput{LeaseID: "L1", RentCents: 1, NewEndDay: 900})
	if errorCode(t, err) != InvalidState {
		t.Fatalf("pending offer/state before cap, got %v", err)
	}
	_, err = service.TenantRespond(321, RespondInput{LeaseID: "L1", OfferID: offer.ID, Decision: Accept})
	if errorCode(t, err) != LateResponse {
		t.Fatalf("late response, got %v", err)
	}

	after, _ := service.Snapshot(9, "L1")
	if after.Lease != before.Lease || service.lastNow != 310 {
		t.Fatalf("rejected operation changed state: before %+v after %+v lastNow %d", before.Lease, after.Lease, service.lastNow)
	}
}

func mustIssue(t *testing.T, service *Service, day, rent int) Offer {
	t.Helper()
	snapshot, err := service.IssueOffer(day, IssueOfferInput{LeaseID: "L1", RentCents: rent, NewEndDay: day + 400})
	if err != nil {
		t.Fatalf("issue offer: %v", err)
	}
	if snapshot.CurrentOffer == nil {
		t.Fatal("issue returned no current offer")
	}
	return *snapshot.CurrentOffer
}
