package ncdengine

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{RenewalGraceDays: 30, AtFaultThreshold: 50, ProtectionStart: 3, MaxLevel: 6, Premiums: []int{1000, 900, 800, 700, 600, 500, 400}}
}

func renewAt(t *testing.T, engine *Engine, customerID string, vehicleID string, day int) {
	t.Helper()
	if _, err := engine.Renew(RenewInput{CustomerID: customerID, VehicleID: vehicleID, Day: day}); err != nil {
		t.Fatalf("renew day %d: %v", day, err)
	}
}

func claim(day int, liability int, id string) ClaimInput {
	return ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: id, AccidentDay: day, Liability: liability, ReportDay: day}
}

func TestRenewalWindowsInterruptionAndEarlyStart(t *testing.T) {
	engine := NewEngine(0)
	if _, err := engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 334}); !errors.Is(err, ErrOutsideRenewalWindow) {
		t.Fatalf("left edge: %v", err)
	}
	early, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 335})
	if err != nil || early.StartDay != 365 || early.Level != 1 {
		t.Fatalf("left equal renewal = %+v, %v", early, err)
	}
	right, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 760})
	if err != nil || right.StartDay != 730 || right.Level != 2 {
		t.Fatalf("right equal renewal = %+v, %v", right, err)
	}
	if _, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 1126}); !errors.Is(err, ErrOutsideRenewalWindow) {
		t.Fatalf("outside window: %v", err)
	}
	if _, err := engine.NewPolicy(NewPolicyInput{CustomerID: "c", VehicleID: "w", Day: 1126}); err != nil {
		t.Fatal(err)
	}
	if level, _ := engine.Level("c"); level != 0 {
		t.Fatalf("interrupted level = %d", level)
	}
}

func TestClaimLevelRulesAndMaxLevel(t *testing.T) {
	engine := NewEngine(0)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()})
	renewAt(t, engine, "c", "v", 335)
	renewAt(t, engine, "c", "v", 700)
	if err := engine.ReportClaim(claim(731, 50, "a")); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReportClaim(claim(732, 49, "b")); err != nil {
		t.Fatal(err)
	}
	renewAt(t, engine, "c", "v", 1065)
	if level, _ := engine.Level("c"); level != 0 {
		t.Fatalf("threshold equal and no-fault = %d", level)
	}

	for _, day := range []int{1430, 1795, 2160, 2525, 2890, 3255, 3620} {
		renewAt(t, engine, "c", "v", day)
	}
	if level, _ := engine.Level("c"); level != 6 {
		t.Fatalf("setup max level = %d", level)
	}
	renewAt(t, engine, "c", "v", 3985)
	if level, _ := engine.Level("c"); level != 6 {
		t.Fatalf("max level advanced = %d", level)
	}
	engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "two-1", AccidentDay: 4020, Liability: 100, ReportDay: 4020})
	engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "two-2", AccidentDay: 4021, Liability: 100, ReportDay: 4021})
	renewAt(t, engine, "c", "v", 4350)
	if level, _ := engine.Level("c"); level != 2 {
		t.Fatalf("two at-fault drops four = %d", level)
	}
	engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "three-1", AccidentDay: 4390, Liability: 100, ReportDay: 4390})
	engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "three-2", AccidentDay: 4391, Liability: 100, ReportDay: 4391})
	engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "three-3", AccidentDay: 4392, Liability: 100, ReportDay: 4392})
	renewAt(t, engine, "c", "v", 4715)
	if level, _ := engine.Level("c"); level != 0 {
		t.Fatalf("three at-fault = %d", level)
	}
}

func TestProtectionBoundariesAndThreeClaimCount(t *testing.T) {
	engine := NewEngine(0)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()})
	if err := engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: "v", Day: 1}); !errors.Is(err, ErrLevelTooLow) {
		t.Fatalf("below protection start: %v", err)
	}
	renewAt(t, engine, "c", "v", 335)
	renewAt(t, engine, "c", "v", 700)
	renewAt(t, engine, "c", "v", 1065)
	if err := engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: "v", Day: 1095}); err != nil {
		t.Fatal(err)
	}
	if err := engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: "v", Day: 1096}); !errors.Is(err, ErrProtectionPurchased) {
		t.Fatalf("duplicate protection: %v", err)
	}
	engine.ReportClaim(claim(1100, 100, "first"))
	engine.ReportClaim(claim(1110, 100, "second"))
	renewAt(t, engine, "c", "v", 1430)
	if level, _ := engine.Level("c"); level != 1 {
		t.Fatalf("protection waived first = %d", level)
	}

	renewAt(t, engine, "c", "v", 1795)
	renewAt(t, engine, "c", "v", 2160)
	renewAt(t, engine, "c", "v", 2525)
	renewAt(t, engine, "c", "v", 2890)
	if err := engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: "v", Day: 2925}); err != nil {
		t.Fatal(err)
	}
	engine.ReportClaim(claim(2930, 100, "p1"))
	engine.ReportClaim(claim(2931, 100, "p2"))
	engine.ReportClaim(claim(2932, 100, "p3"))
	renewAt(t, engine, "c", "v", 3255)
	if level, _ := engine.Level("c"); level != 0 {
		t.Fatalf("protection still counts three = %d", level)
	}
}

func TestTransferInheritsCurrentYearClaims(t *testing.T) {
	engine := NewEngine(0)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()})
	renewAt(t, engine, "c", "v", 335)
	engine.ReportClaim(claim(400, 100, "carry"))
	if _, err := engine.Transfer(TransferInput{CustomerID: "c", FromVehicleID: "v", ToVehicleID: "n", Day: 401}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewPolicy(NewPolicyInput{CustomerID: "c", VehicleID: "o", Day: 402}); !errors.Is(err, ErrActivePolicyExists) {
		t.Fatalf("active after transfer: %v", err)
	}
	if _, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "n", Day: 700}); err != nil {
		t.Fatal(err)
	}
	if level, _ := engine.Level("c"); level != 0 {
		t.Fatalf("inherited current-year claim level = %d", level)
	}
}

func TestLateReportRecomputeDeltasAndDeletion(t *testing.T) {
	engine := NewEngine(0)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()})
	renewAt(t, engine, "c", "v", 335)
	renewAt(t, engine, "c", "v", 700)
	before, _ := engine.Terms("c")
	if err := engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "late", AccidentDay: 10, Liability: 100, ReportDay: 800}); err != nil {
		t.Fatal(err)
	}
	after, _ := engine.Terms("c")
	if after[1].RenewalLevel != 0 || after[2].RenewalLevel != 1 {
		t.Fatalf("recomputed levels = %d,%d", after[1].RenewalLevel, after[2].RenewalLevel)
	}
	if before[1].RenewalLevel != 1 || before[2].RenewalLevel != 2 {
		t.Fatalf("old trajectory snapshot = %d,%d", before[1].RenewalLevel, before[2].RenewalLevel)
	}
	deltas, _ := engine.PremiumDeltas("c")
	if len(deltas) != 2 || deltas[0].Amount != 100 || deltas[0].StartDay != 365 || deltas[1].Amount != 100 || deltas[1].StartDay != 730 {
		t.Fatalf("premium deltas = %+v", deltas)
	}
	if err := engine.DeleteClaim(DeleteClaimInput{CustomerID: "c", ClaimID: "late", Day: 801}); err != nil {
		t.Fatal(err)
	}
	final, _ := engine.Terms("c")
	if final[1].RenewalLevel != 1 || final[2].RenewalLevel != 2 {
		t.Fatalf("delete equivalent to never registered = %d,%d", final[1].RenewalLevel, final[2].RenewalLevel)
	}
}

func TestRejectionOrderAndNoTrace(t *testing.T) {
	engine := NewEngine(10)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 10, Config: testConfig()})
	engine.ReportClaim(claim(11, 0, "dup"))
	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"参数非法", ErrInvalidArgument, func() error { _, err := engine.Renew(RenewInput{Day: 11}); return err }},
		{"被保人不存在", ErrCustomerNotFound, func() error { _, err := engine.Renew(RenewInput{CustomerID: "x", VehicleID: "v", Day: 11}); return err }},
		{"时钟回退", ErrClockMovedBack, func() error { _, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 10}); return err }},
		{"已有在保保单", ErrActivePolicyExists, func() error {
			_, err := engine.NewPolicy(NewPolicyInput{CustomerID: "c", VehicleID: "w", Day: 11})
			return err
		}},
		{"事故已存在", ErrClaimExists, func() error { return engine.ReportClaim(claim(11, 0, "dup")) }},
		{"事故不存在", ErrClaimNotFound, func() error {
			return engine.DeleteClaim(DeleteClaimInput{CustomerID: "c", ClaimID: "missing", Day: 12})
		}},
		{"事故日未承保", ErrAccidentUncovered, func() error {
			return engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "u", AccidentDay: 500, Liability: 0, ReportDay: 500})
		}},
		{"等级不足", ErrLevelTooLow, func() error { return engine.BuyProtection(ProtectInput{CustomerID: "c", VehicleID: "v", Day: 12}) }},
		{"续保窗口外", ErrOutsideRenewalWindow, func() error { _, err := engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 13}); return err }},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s = %v, want %v", tc.name, err, tc.want)
		}
	}
	terms, _ := engine.Terms("c")
	claims, _ := engine.Claims("c")
	deltas, _ := engine.PremiumDeltas("c")
	if len(terms) != 1 || len(claims) != 1 || len(deltas) != 0 || engine.Clock() != 11 {
		t.Fatalf("rejected operations left trace: terms=%d claims=%d deltas=%d clock=%d", len(terms), len(claims), len(deltas), engine.Clock())
	}
}
