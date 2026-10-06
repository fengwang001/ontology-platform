package mileage

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		MinimumBaseMiles:   10,
		RetroWindowSeconds: 7,
		PeriodSeconds:      10,
		Thresholds:         [3]int{100, 200, 300},
		BonusPercent:       [4]int{0, 10, 20, 30},
		InactivitySeconds:  20,
		CancelFeeMiles:     5,
	}
}

func newTestService(t *testing.T, config Config) *Service {
	t.Helper()
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := service.CreateAccount("a", 0); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return service
}

func mustCredit(t *testing.T, service *Service, now int64, id string, distance, percent int, flownAt int64) CreditResult {
	t.Helper()
	result, err := service.Credit(CreditInput{
		AccountID: "a",
		Now:       now,
		Segment:   Segment{ID: id, Distance: distance, FarePercent: percent, FlownAt: flownAt},
	})
	if err != nil {
		t.Fatalf("Credit %s at %d: %v", id, now, err)
	}
	return result
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestThresholdExactUpgradeUsesPreviousLevelForBonus(t *testing.T) {
	service := newTestService(t, testConfig())

	first := mustCredit(t, service, 1, "s1", 1000, 9, 0)
	if first.BaseMiles != 90 || first.QualifyingMiles != 90 || first.GrossRedeemable != 90 ||
		first.LevelBefore != 0 || first.LevelAfter != 0 {
		t.Fatalf("first credit = %+v", first)
	}
	second := mustCredit(t, service, 2, "s2", 100, 10, 1)
	if second.BaseMiles != 10 || second.QualifyingMiles != 10 || second.GrossRedeemable != 10 ||
		second.LevelBefore != 0 || second.LevelAfter != 1 {
		t.Fatalf("threshold credit = %+v", second)
	}
	third := mustCredit(t, service, 3, "s3", 100, 10, 2)
	if third.GrossRedeemable != 11 || third.LevelBefore != 1 || third.LevelAfter != 1 {
		t.Fatalf("upgraded credit = %+v", third)
	}

	view, err := service.View("a")
	if err != nil {
		t.Fatal(err)
	}
	if view.Level != 1 || view.QualifyingMiles != 110 || view.RedeemableMiles != 111 {
		t.Fatalf("view = %+v", view)
	}
}

func TestCeilingAndMinimumBaseMiles(t *testing.T) {
	service := newTestService(t, testConfig())
	result := mustCredit(t, service, 0, "s", 99, 1, 0)
	if result.BaseMiles != 10 || result.GrossRedeemable != 10 {
		t.Fatalf("result = %+v", result)
	}
}

func TestLateCreditIntoClosedPeriodDoesNotUpgrade(t *testing.T) {
	service := newTestService(t, testConfig())
	mustCredit(t, service, 9, "s1", 90, 100, 9)
	late := mustCredit(t, service, 16, "s2", 10, 100, 9)
	if late.Period != 0 || late.LevelBefore != 0 || late.LevelAfter != 0 {
		t.Fatalf("late = %+v", late)
	}
	view, _ := service.ViewAt("a", 16)
	if view.Level != 0 || view.CurrentPeriod != 1 || view.QualifyingMiles != 0 {
		t.Fatalf("view = %+v", view)
	}
	_, err := service.Credit(CreditInput{AccountID: "a", Now: 17, Segment: Segment{
		ID: "s3", Distance: 10, FarePercent: 100, FlownAt: 9,
	}})
	assertErrorIs(t, err, ErrLateCredit)
}

func TestPeriodEndDowngradeAtMostOneLevel(t *testing.T) {
	service := newTestService(t, testConfig())
	mustCredit(t, service, 1, "s1", 300, 100, 1)
	if view, _ := service.View("a"); view.Level != 3 {
		t.Fatalf("level = %d", view.Level)
	}
	mustCredit(t, service, 11, "s2", 150, 100, 11)
	view, _ := service.ViewAt("a", 11)
	if view.Level != 3 || view.QualifyingMiles != 150 {
		t.Fatalf("after one closed period view = %+v", view)
	}
	view, _ = service.ViewAt("a", 21)
	if view.Level != 2 {
		t.Fatalf("after 150-mile period level = %d", view.Level)
	}
	view, _ = service.ViewAt("a", 31)
	if view.Level != 1 {
		t.Fatalf("after empty period level = %d", view.Level)
	}
	view, _ = service.ViewAt("a", 41)
	if view.Level != 0 {
		t.Fatalf("floor after three empty periods level = %d", view.Level)
	}
}

func TestRefundCreatesDebtThenCreditRepaysFirst(t *testing.T) {
	config := testConfig()
	config.BonusPercent = [4]int{}
	service := newTestService(t, config)
	mustCredit(t, service, 0, "s1", 100, 100, 0)
	if _, err := service.Redeem(RedeemInput{AccountID: "a", RedemptionID: "used", CostMiles: 100, Now: 0}); err != nil {
		t.Fatal(err)
	}
	refund, err := service.Refund(RefundInput{AccountID: "a", Now: 1, SegmentID: "s1"})
	if err != nil || !refund.Posted || refund.DebtCreated != 100 {
		t.Fatalf("refund = %+v, err = %v", refund, err)
	}
	view, _ := service.View("a")
	if view.RedeemableMiles != 0 || view.DebtMiles != 100 {
		t.Fatalf("after refund view = %+v", view)
	}
	result := mustCredit(t, service, 2, "s2", 60, 100, 2)
	if result.DebtRepaid != 60 || result.RedeemableAdded != 0 {
		t.Fatalf("partial debt credit = %+v", result)
	}
	result = mustCredit(t, service, 3, "s3", 50, 100, 3)
	if result.DebtRepaid != 40 || result.RedeemableAdded != 10 {
		t.Fatalf("debt payoff credit = %+v", result)
	}
	view, _ = service.View("a")
	if view.RedeemableMiles != 10 || view.DebtMiles != 0 {
		t.Fatalf("final view = %+v", view)
	}
}

func TestUnknownRefundBlocksLaterCreditWithoutError(t *testing.T) {
	service := newTestService(t, testConfig())
	refund, err := service.Refund(RefundInput{AccountID: "a", Now: 0, SegmentID: "x"})
	if err != nil || refund.Seen || refund.Posted {
		t.Fatalf("refund = %+v, err = %v", refund, err)
	}
	_, err = service.Credit(CreditInput{AccountID: "a", Now: 0, Segment: Segment{
		ID: "x", Distance: 10, FarePercent: 100, FlownAt: 0,
	}})
	assertErrorIs(t, err, ErrDuplicateCredit)
}

func TestCancelRedemptionFeeNotSmallerThanCost(t *testing.T) {
	service := newTestService(t, testConfig())
	mustCredit(t, service, 0, "s", 100, 100, 0)
	if _, err := service.Redeem(RedeemInput{AccountID: "a", RedemptionID: "r", CostMiles: 5, Now: 0}); err != nil {
		t.Fatal(err)
	}
	cancel, err := service.CancelRedemption(CancelRedemptionInput{AccountID: "a", RedemptionID: "r", Now: 1})
	if err != nil || cancel.Refunded != 0 || cancel.AddedToBalance != 0 {
		t.Fatalf("cancel = %+v, err = %v", cancel, err)
	}
	_, err = service.CancelRedemption(CancelRedemptionInput{AccountID: "a", RedemptionID: "r", Now: 2})
	assertErrorIs(t, err, ErrRedemptionCanceled)
}

func TestInactivityAtExactlyThresholdFreezesCreditButRefundRuns(t *testing.T) {
	service := newTestService(t, testConfig())
	mustCredit(t, service, 0, "s1", 100, 100, 0)
	_, err := service.Credit(CreditInput{AccountID: "a", Now: 20, Segment: Segment{
		ID: "s2", Distance: 10, FarePercent: 100, FlownAt: 20,
	}})
	assertErrorIs(t, err, ErrAccountFrozen)
	refund, err := service.Refund(RefundInput{AccountID: "a", Now: 20, SegmentID: "s1"})
	if err != nil || !refund.Posted {
		t.Fatalf("frozen refund = %+v, err = %v", refund, err)
	}
	if err := service.Unfreeze(UnfreezeInput{AccountID: "a", Now: 20}); err != nil {
		t.Fatal(err)
	}
	mustCredit(t, service, 20, "s2", 10, 100, 20)
}

func TestRejectedOperationDoesNotAdvanceClock(t *testing.T) {
	service := newTestService(t, testConfig())
	_, err := service.Credit(CreditInput{AccountID: "a", Now: 10, Segment: Segment{
		ID: "s", Distance: 10, FarePercent: 100, FlownAt: 0,
	}})
	assertErrorIs(t, err, ErrLateCredit)
	mustCredit(t, service, 0, "s", 10, 100, 0)
	_, err = service.Credit(CreditInput{AccountID: "missing", Now: -1, Segment: Segment{
		ID: "s", Distance: 10, FarePercent: 100, FlownAt: 0,
	}})
	assertErrorIs(t, err, ErrInvalidArgument)
	_, err = service.Credit(CreditInput{AccountID: "missing", Now: 0, Segment: Segment{
		ID: "s", Distance: 10, FarePercent: 100, FlownAt: 0,
	}})
	assertErrorIs(t, err, ErrAccountNotFound)
	_, err = service.Credit(CreditInput{AccountID: "missing", Now: 0, Segment: Segment{
		ID: "s", Distance: 10, FarePercent: 100, FlownAt: 0,
	}})
	assertErrorIs(t, err, ErrAccountNotFound)
}

func TestAdjacentRejectionCategories(t *testing.T) {
	good := Segment{ID: "s", Distance: 10, FarePercent: 100, FlownAt: 0}
	tests := []struct {
		name string
		call func(service *Service) error
		want error
	}{
		{"invalid before clock", func(service *Service) error {
			_, err := service.Credit(CreditInput{AccountID: "a", Now: -1, Segment: good})
			return err
		}, ErrInvalidArgument},
		{"clock before missing", func(service *Service) error {
			_, err := service.Credit(CreditInput{AccountID: "missing", Now: -1, Segment: good})
			return err
		}, ErrInvalidArgument},
		{"clock before frozen", func(service *Service) error {
			_, err := service.Credit(CreditInput{AccountID: "a", Now: -1, Segment: Segment{
				ID: "f", Distance: 10, FarePercent: 100, FlownAt: 0,
			}})
			return err
		}, ErrInvalidArgument},
		{"frozen before duplicate", func(service *Service) error {
			mustCredit(t, service, 0, "dup", 10, 100, 0)
			_, err := service.Credit(CreditInput{AccountID: "a", Now: 20, Segment: Segment{
				ID: "dup", Distance: 10, FarePercent: 100, FlownAt: 20,
			}})
			return err
		}, ErrAccountFrozen},
		{"duplicate before late", func(service *Service) error {
			mustCredit(t, service, 0, "dup2", 10, 100, 0)
			_, err := service.Credit(CreditInput{AccountID: "a", Now: 9, Segment: Segment{
				ID: "dup2", Distance: 10, FarePercent: 100, FlownAt: 0,
			}})
			return err
		}, ErrDuplicateCredit},
		{"record missing before canceled", func(service *Service) error {
			_, err := service.CancelRedemption(CancelRedemptionInput{AccountID: "a", RedemptionID: "no", Now: 0})
			return err
		}, ErrRedemptionNotFound},
		{"canceled before insufficient", func(service *Service) error {
			mustCredit(t, service, 0, "x", 1, 100, 0)
			if _, err := service.Redeem(RedeemInput{AccountID: "a", RedemptionID: "r", CostMiles: 1, Now: 0}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CancelRedemption(CancelRedemptionInput{AccountID: "a", RedemptionID: "r", Now: 1}); err != nil {
				t.Fatal(err)
			}
			_, err := service.CancelRedemption(CancelRedemptionInput{AccountID: "a", RedemptionID: "r", Now: 2})
			return err
		}, ErrRedemptionCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(t, testConfig())
			assertErrorIs(t, test.call(service), test.want)
		})
	}
}

func TestConcurrentAcceptedOperationsNeverCorruptInvariants(t *testing.T) {
	config := testConfig()
	config.InactivitySeconds = 1_000_000
	service := newTestService(t, config)
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 20; i++ {
				now := int64(0)
				_, _ = service.Credit(CreditInput{
					AccountID: "a",
					Now:       now,
					Segment: Segment{
						ID:          fmt.Sprintf("c-%d-%d", worker, i),
						Distance:    1,
						FarePercent: 100,
						FlownAt:     now,
					},
				})
			}
		}(worker)
	}
	wait.Wait()
	view, err := service.View("a")
	if err != nil {
		t.Fatal(err)
	}
	if view.RedeemableMiles < 0 || view.DebtMiles < 0 ||
		(view.RedeemableMiles > 0 && view.DebtMiles > 0) {
		t.Fatalf("invalid balance/debt state: %+v", view)
	}
}
