package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestLimitBoundaryAndBreachedPartyTrading(t *testing.T) {
	controller := newController(t, 0)
	must(t, controller.Register("C", 100))
	must(t, controller.SetPrice("X", 10))

	must(t, controller.Trade("C", "X", 10))
	if got := mustExposure(t, controller, "C"); got != 100 {
		t.Fatalf("exposure at limit: got %d", got)
	}
	wantErr(t, controller.Trade("C", "X", 1), ErrLimitExceeded)

	must(t, controller.SetPrice("X", 11))
	if got := mustExposure(t, controller, "C"); got != 110 {
		t.Fatalf("passive breach exposure: got %d", got)
	}
	must(t, controller.Trade("C", "X", -1))
	if got := mustExposure(t, controller, "C"); got != 99 {
		t.Fatalf("reducing a breached position: got %d", got)
	}
}

func TestBreachedPartyAcceptsEqualExposureAndRejectsIncrease(t *testing.T) {
	controller := newController(t, 10000)
	must(t, controller.Register("C", 100))
	must(t, controller.SetPrice("X", 10))
	must(t, controller.SetPrice("Y", 10))
	must(t, controller.SetGroup("X", "g"))
	must(t, controller.SetGroup("Y", "g"))
	must(t, controller.Trade("C", "X", 10))
	must(t, controller.SetPrice("X", 12))

	before := mustExposure(t, controller, "C")
	must(t, controller.Trade("C", "Y", -1))
	if got := mustExposure(t, controller, "C"); got != before {
		t.Fatalf("hedge buffer did not preserve exposure: got %d, before %d", got, before)
	}
	wantErr(t, controller.Trade("C", "X", 1), ErrLimitExceeded)
	if got := mustExposure(t, controller, "C"); got != before {
		t.Fatalf("rejected trade changed exposure: got %d", got)
	}
}

func TestNegativeMarkedValueAndRelease(t *testing.T) {
	controller := newController(t, 0)
	must(t, controller.Register("C", 100))
	must(t, controller.SetPrice("X", 10))
	must(t, controller.SetPrice("Y", 10))

	must(t, controller.Trade("C", "X", -5))
	if got := mustExposure(t, controller, "C"); got != 0 {
		t.Fatalf("negative marked value exposure: got %d", got)
	}
	must(t, controller.Trade("C", "Y", 8))
	if got := mustExposure(t, controller, "C"); got != 30 {
		t.Fatalf("offsetting cross-instrument purchase: got %d", got)
	}

	must(t, controller.Post("C", 30))
	if got := mustExposure(t, controller, "C"); got != 0 {
		t.Fatalf("fully collateralized exposure: got %d", got)
	}
	must(t, controller.Release("C", 30))
	if got := mustExposure(t, controller, "C"); got != 30 {
		t.Fatalf("release did not restore exposure: got %d", got)
	}
	wantErr(t, controller.Release("C", 1), ErrInsufficientCollateral)

	controller2 := newController(t, 10000)
	must(t, controller2.Register("D", 100))
	must(t, controller2.SetPrice("X", 10))
	must(t, controller2.Trade("D", "X", -10))
	must(t, controller2.Post("D", 100))
	if got := mustExposure(t, controller2, "D"); got != 0 {
		t.Fatalf("fully hedged collateralized starting exposure: got %d", got)
	}
	must(t, controller2.Release("D", 10))
	if got := mustExposure(t, controller2, "D"); got != 0 {
		t.Fatalf("non-increasing release while floored at zero: got %d", got)
	}
}

func TestNegativeMarkedValueIncreaseRejectedWhenOverLimit(t *testing.T) {
	controller := newController(t, 0)
	must(t, controller.Register("C", 100))
	must(t, controller.SetPrice("X", 10))
	must(t, controller.SetPrice("Y", 10))
	must(t, controller.Trade("C", "X", -20))
	must(t, controller.Trade("C", "Y", 5))
	if got := mustExposure(t, controller, "C"); got != 0 {
		t.Fatalf("negative net exposure: got %d", got)
	}
	wantErr(t, controller.Trade("C", "Y", 26), ErrLimitExceeded)
}

func TestPerGroupRoundingAndGroupMembership(t *testing.T) {
	grouped := newController(t, 1000)
	must(t, grouped.Register("C", 1_000_000_000_000_000))
	must(t, grouped.SetPrice("X", 10))
	must(t, grouped.SetPrice("V", 11))
	must(t, grouped.SetPrice("Z1", 7))
	must(t, grouped.SetPrice("Z2", 3))
	must(t, grouped.SetGroup("X", "g1"))
	must(t, grouped.SetGroup("V", "g1"))
	must(t, grouped.SetGroup("Z1", "g2"))
	must(t, grouped.SetGroup("Z2", "g2"))
	must(t, grouped.Trade("C", "V", -5))
	must(t, grouped.Trade("C", "X", 8))
	must(t, grouped.Trade("C", "Z2", -4))
	must(t, grouped.Trade("C", "Z1", 5))
	if got := mustExposure(t, grouped, "C"); got != 56 {
		t.Fatalf("two-group per-group ceiling: got %d", got)
	}

	separate := newController(t, 1000)
	must(t, separate.Register("C", 1_000_000_000_000_000))
	for _, setting := range []struct {
		name  string
		price int64
	}{{"X", 10}, {"V", 11}, {"Z1", 7}, {"Z2", 3}} {
		must(t, separate.SetPrice(setting.name, setting.price))
	}
	must(t, separate.Trade("C", "X", 8))
	must(t, separate.Trade("C", "V", -5))
	must(t, separate.Trade("C", "Z1", 5))
	must(t, separate.Trade("C", "Z2", -4))
	if got := mustExposure(t, separate, "C"); got != 48 {
		t.Fatalf("singleton groups have no hedge buffer: got %d", got)
	}

	fullBuffer := newController(t, 10000)
	must(t, fullBuffer.Register("C", 1_000_000_000_000_000))
	must(t, fullBuffer.SetPrice("X", 10))
	must(t, fullBuffer.SetPrice("Y", 10))
	must(t, fullBuffer.SetGroup("X", "g"))
	must(t, fullBuffer.SetGroup("Y", "g"))
	must(t, fullBuffer.Trade("C", "X", 5))
	must(t, fullBuffer.Trade("C", "Y", -5))
	if got := mustExposure(t, fullBuffer, "C"); got != 50 {
		t.Fatalf("full basis buffer: got %d", got)
	}
}

func TestSetGroupPassivelyBreachesAndSequenceRules(t *testing.T) {
	controller := newController(t, 10000)
	must(t, controller.Register("A", 100))
	must(t, controller.Register("B", 100))
	must(t, controller.SetPrice("X", 10))
	must(t, controller.SetPrice("Y", 10))
	must(t, controller.Post("A", 50))
	must(t, controller.Post("B", 50))
	must(t, controller.Trade("A", "X", 15))
	must(t, controller.Trade("A", "Y", -15))
	must(t, controller.Trade("B", "Y", 15))
	must(t, controller.Trade("B", "X", -15))
	must(t, controller.Release("A", 49))
	must(t, controller.Release("B", 49))

	must(t, controller.SetGroup("X", "g"))
	must(t, controller.SetGroup("Y", "g"))
	breaches := controller.Breaches()
	if len(breaches) != 2 || breaches[0].Counterparty != "A" || breaches[1].Counterparty != "B" {
		t.Fatalf("simultaneous breaches ordered by byte order: %+v", breaches)
	}

	must(t, controller.Post("A", 150))
	must(t, controller.Post("B", 150))
	if len(controller.Breaches()) != 0 {
		t.Fatalf("collateral did not clear breaches")
	}

	must(t, controller.SetPrice("X", 20))
	must(t, controller.SetPrice("Y", 20))
	breaches = controller.Breaches()
	if len(breaches) != 2 || breaches[0].Counterparty != "A" || breaches[1].Counterparty != "B" {
		t.Fatalf("re-entry did not allocate fresh byte-ordered sequences: %+v", breaches)
	}

	must(t, controller.Post("A", 300))
	must(t, controller.SetPrice("X", 30))
	must(t, controller.SetPrice("Y", 30))
	must(t, controller.SetPrice("X", 40))
	must(t, controller.SetPrice("Y", 40))
	breaches = controller.Breaches()
	if len(breaches) != 2 || breaches[0].Counterparty != "B" || breaches[1].Counterparty != "A" {
		t.Fatalf("persistent B should precede re-entered A: %+v", breaches)
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	controller := newController(t, 0)
	must(t, controller.Register("C", 100))
	must(t, controller.SetPrice("X", 10))
	must(t, controller.Trade("C", "X", 1))

	wantErr(t, controller.Register("C", 0), ErrCounterpartyAlreadyRegistered)
	wantErr(t, controller.Register("C", 100), ErrCounterpartyAlreadyRegistered)
	wantErr(t, controller.Register("", 0), ErrInvalidArgument)
	wantErr(t, controller.Trade("C", "X", 0), ErrInvalidArgument)
	wantErr(t, controller.Trade("Z", "X", 1), ErrCounterpartyNotFound)
	wantErr(t, controller.Trade("C", "Z", 1), ErrNoPrice)
	wantErr(t, controller.SetGroup("Z", "g"), ErrNoPrice)
	wantErr(t, controller.SetGroup("", "g"), ErrInvalidArgument)
	wantErr(t, controller.Post("Z", 1), ErrCounterpartyNotFound)
	wantErr(t, controller.Release("Z", 1), ErrCounterpartyNotFound)
	wantErr(t, controller.Release("C", 1), ErrInsufficientCollateral)

	if got := mustExposure(t, controller, "C"); got != 10 {
		t.Fatalf("rejected operations changed exposure: %d", got)
	}
}

func TestConcurrentOperations(t *testing.T) {
	controller := newController(t, 0)
	must(t, controller.Register("C", 1_000_000_000_000_000))
	must(t, controller.SetPrice("X", 1))

	var waitGroup sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for iteration := 0; iteration < 100; iteration++ {
				switch iteration % 4 {
				case 0:
					_ = controller.Trade("C", "X", 1)
				case 1:
					_ = controller.Trade("C", "X", -1)
				case 2:
					_ = controller.Post("C", 1)
				default:
					_, _ = controller.Exposure("C")
				}
			}
		}()
	}
	waitGroup.Wait()

	if exposure := mustExposure(t, controller, "C"); exposure < 0 {
		t.Fatalf("concurrent execution produced negative exposure: %d", exposure)
	}
}

func newController(t *testing.T, lambda int) *Controller {
	t.Helper()
	controller, err := New(lambda)
	if err != nil {
		t.Fatalf("New(%d): %v", lambda, err)
	}
	return controller
}

func must(t *testing.T, err error, wanted ...error) {
	t.Helper()
	want := error(nil)
	if len(wanted) > 0 {
		want = wanted[0]
	}
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func wantErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
