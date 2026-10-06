package pharmacy

import (
	"sync"
	"testing"
)

func TestConcurrentReplayEquivalence(t *testing.T) {
	parallel := NewSystem(Options{ReservationWindow: 50})
	mustRegister(t, parallel, "d", 1, true)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		now := i * 2
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = parallel.ReceiveDrug("d", now, 1)
		}()
		go func() {
			defer wg.Done()
			id := rxID(i)
			input := PrescriptionInput{PatientID: "p", IssuedAt: now, Lines: []LineInput{{DrugID: "d", Quantity: 1}}}
			_ = parallel.AcceptPrescription(id, input, now)
		}()
	}
	wg.Wait()
	parallelReport, _ := parallel.Drug("d", 78)
	if parallelReport.ActiveReservation < 0 || parallelReport.OutstandingDebt < 0 ||
		parallelReport.OnHand < parallelReport.ActiveReservation {
		t.Fatalf("invalid report=%+v", parallelReport)
	}
}

func rxID(i int) string {
	return "rx-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
}
