package ncdengine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentLateClaimAndRenewalIsSerializable(t *testing.T) {
	engine := NewEngine(0)
	engine.Register(RegisterInput{CustomerID: "c", VehicleID: "v", StartDay: 0, Config: testConfig()})
	renewAt(t, engine, "c", "v", 335)

	var wg sync.WaitGroup
	wg.Add(2)
	var claimErr error
	var renewErr error
	go func() {
		defer wg.Done()
		claimErr = engine.ReportClaim(ClaimInput{CustomerID: "c", VehicleID: "v", ClaimID: "late", AccidentDay: 10, Liability: 100, ReportDay: 336})
	}()
	go func() {
		defer wg.Done()
		_, renewErr = engine.Renew(RenewInput{CustomerID: "c", VehicleID: "v", Day: 700})
	}()
	wg.Wait()
	if renewErr != nil {
		t.Fatalf("concurrent operations: claim=%v renewal=%v", claimErr, renewErr)
	}

	terms, _ := engine.Terms("c")
	deltas, _ := engine.PremiumDeltas("c")
	if claimErr == nil {
		if terms[1].RenewalLevel != 0 || terms[2].RenewalLevel != 1 {
			t.Fatalf("claim-first trajectory = %d,%d", terms[1].RenewalLevel, terms[2].RenewalLevel)
		}
		if len(deltas) != 1 || deltas[0].StartDay != 365 || deltas[0].Amount != 100 {
			t.Fatalf("claim-first delta = %+v", deltas)
		}
		return
	}
	if !errors.Is(claimErr, ErrClockMovedBack) {
		t.Fatalf("renewal-first should reject late report with clock rollback, got %v", claimErr)
	}
	if terms[1].RenewalLevel != 1 || terms[2].RenewalLevel != 2 {
		t.Fatalf("renewal-first trajectory = %d,%d", terms[1].RenewalLevel, terms[2].RenewalLevel)
	}
	if len(deltas) != 0 {
		t.Fatalf("renewal-first delta = %+v", deltas)
	}
}

func buildHistory(b *testing.B, years int) *account {
	b.Helper()
	customer := newAccount(testConfig())
	customer.terms = append(customer.terms, PolicyTerm{VehicleID: "v", StartDay: 0, EndDay: 365})
	for year := 0; year < years; year++ {
		last := customer.terms[year]
		level := nextLevel(last.RenewalLevel, customer.termClaims[last.StartDay], last.Protection, customer.config)
		customer.terms = append(customer.terms, PolicyTerm{VehicleID: "v", StartDay: last.EndDay, EndDay: last.EndDay + 365, InitialLevel: last.RenewalLevel, RenewalLevel: level})
	}
	return customer
}

func BenchmarkRenewalGradingWithLongHistory(b *testing.B) {
	for _, years := range []int{100, 500} {
		b.Run(fmt.Sprintf("%d-years", years), func(b *testing.B) {
			customer := buildHistory(b, years)
			last := customer.terms[len(customer.terms)-1]
			b.ResetTimer()
			for range b.N {
				level := nextLevel(last.RenewalLevel, customer.termClaims[last.StartDay], last.Protection, customer.config)
				if level < 0 {
					b.Fatal(level)
				}
			}
		})
	}
}
