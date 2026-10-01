package ontology

import (
	"errors"
	"testing"
)

func checkStep(t *testing.T, step string, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", step, got, want)
	}
	t.Logf("%s => result=%v", step, got)
}

func TestExampleOne(t *testing.T) {
	controller, err := New(0)
	if err != nil {
		t.Fatalf("New(0) returned %v", err)
	}

	checkStep(t, "Register(C,100)", controller.Register("C", 100), nil)
	checkStep(t, "SetPrice(X,10)", controller.SetPrice("X", 10), nil)
	checkStep(t, "SetPrice(Y,20)", controller.SetPrice("Y", 20), nil)
	checkStep(t, "Trade(C,X,+8)", controller.Trade("C", "X", 8), nil)
	checkStep(t, "Trade(C,Y,+2)", controller.Trade("C", "Y", 2), ErrLimitExceeded)

	if exposure := mustExposure(t, controller, "C"); exposure != 80 {
		t.Fatalf("exposure before reprice: got %d, want 80", exposure)
	}

	checkStep(t, "SetPrice(X,15)", controller.SetPrice("X", 15), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 120 {
		t.Fatalf("exposure after reprice: got %d, want 120", exposure)
	}
	if breaches := controller.Breaches(); len(breaches) != 1 ||
		breaches[0].Counterparty != "C" || breaches[0].Exposure != 120 {
		t.Fatalf("breaches after reprice: %+v", breaches)
	}

	checkStep(t, "Trade(C,X,-1)", controller.Trade("C", "X", -1), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 105 {
		t.Fatalf("exposure after reducing position: got %d, want 105", exposure)
	}

	checkStep(t, "Post(C,30)", controller.Post("C", 30), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 75 {
		t.Fatalf("exposure after collateral: got %d, want 75", exposure)
	}
	if breaches := controller.Breaches(); len(breaches) != 0 {
		t.Fatalf("breaches after collateral: %+v", breaches)
	}
}

func TestExampleTwo(t *testing.T) {
	controller, err := New(1000)
	if err != nil {
		t.Fatalf("New(1000) returned %v", err)
	}

	checkStep(t, "Register(C,100)", controller.Register("C", 100), nil)
	checkStep(t, "Register(D,50)", controller.Register("D", 50), nil)
	for _, price := range []struct {
		instrument string
		price      int64
	}{
		{"X", 10},
		{"V", 11},
		{"Z1", 7},
		{"Z2", 3},
	} {
		checkStep(t, "SetPrice("+price.instrument+")", controller.SetPrice(price.instrument, price.price), nil)
	}
	checkStep(t, "SetGroup(X,g1)", controller.SetGroup("X", "g1"), nil)
	checkStep(t, "SetGroup(V,g1)", controller.SetGroup("V", "g1"), nil)
	checkStep(t, "SetGroup(Z1,g2)", controller.SetGroup("Z1", "g2"), nil)
	checkStep(t, "SetGroup(Z2,g2)", controller.SetGroup("Z2", "g2"), nil)

	checkStep(t, "Trade(C,X,+8)", controller.Trade("C", "X", 8), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 80 {
		t.Fatalf("C after X: got %d, want 80", exposure)
	}
	checkStep(t, "Trade(C,V,-5)", controller.Trade("C", "V", -5), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 31 {
		t.Fatalf("C after V: got %d, want 31", exposure)
	}
	checkStep(t, "Trade(C,Z1,+5)", controller.Trade("C", "Z1", 5), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 66 {
		t.Fatalf("C after Z1: got %d, want 66", exposure)
	}
	checkStep(t, "Trade(C,Z2,-4)", controller.Trade("C", "Z2", -4), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 56 {
		t.Fatalf("C after Z2: got %d, want 56", exposure)
	}

	checkStep(t, "Post(C,6)", controller.Post("C", 6), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 50 {
		t.Fatalf("C after collateral: got %d, want 50", exposure)
	}

	checkStep(t, "Trade(D,X,+4)", controller.Trade("D", "X", 4), nil)
	if exposure := mustExposure(t, controller, "D"); exposure != 40 {
		t.Fatalf("D after trade: got %d, want 40", exposure)
	}

	checkStep(t, "SetPrice(X,13)", controller.SetPrice("X", 13), nil)
	if exposure := mustExposure(t, controller, "C"); exposure != 74 {
		t.Fatalf("C after X=13: got %d, want 74", exposure)
	}
	if exposure := mustExposure(t, controller, "D"); exposure != 52 {
		t.Fatalf("D after X=13: got %d, want 52", exposure)
	}
	if breaches := controller.Breaches(); len(breaches) != 1 ||
		breaches[0].Counterparty != "D" || breaches[0].Exposure != 52 {
		t.Fatalf("breaches after X=13: %+v", breaches)
	}

	checkStep(t, "SetPrice(X,20)", controller.SetPrice("X", 20), nil)
	breaches := controller.Breaches()
	wantBreaches := []Breach{{Counterparty: "D", Exposure: 80}, {Counterparty: "C", Exposure: 130}}
	if len(breaches) != len(wantBreaches) {
		t.Fatalf("breaches after X=20: got %+v, want %+v", breaches, wantBreaches)
	}
	for i := range wantBreaches {
		if breaches[i] != wantBreaches[i] {
			t.Fatalf("breach %d: got %+v, want %+v", i, breaches[i], wantBreaches[i])
		}
	}
}

func mustExposure(t *testing.T, controller *Controller, counterparty string) int64 {
	t.Helper()
	exposure, err := controller.Exposure(counterparty)
	if err != nil {
		t.Fatalf("Exposure(%s): %v", counterparty, err)
	}
	t.Logf("Exposure(%s)=%d", counterparty, exposure)
	return exposure
}
