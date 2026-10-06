package pharmacy

import (
	"strconv"
	"testing"
)

func buildBenchmarkSystem(b *testing.B, historical int) (*System, []string) {
	b.Helper()
	s := NewSystem(Options{ReservationWindow: 100000})
	if err := s.RegisterDrug("cold", DrugConfig{PackSize: 1, Splittable: true}); err != nil {
		b.Fatal(err)
	}
	if err := s.ReceiveDrug("cold", 0, historical); err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 0, historical)
	for i := 0; i < historical; i++ {
		id := "cold-" + strconv.Itoa(i)
		input := PrescriptionInput{
			PatientID: "patient",
			IssuedAt:  0,
			Lines:     []LineInput{{DrugID: "cold", Quantity: 1}},
		}
		if err := s.AcceptPrescription(id, input, 0); err != nil {
			b.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if _, err := s.Dispense(id, 1); err != nil {
			b.Fatal(err)
		}
	}
	return s, ids
}

func BenchmarkAffectedDrugOnlySmall(b *testing.B) {
	benchmarkAffectedDrugOnly(b, 1_000)
}

func BenchmarkAffectedDrugOnlyLarge(b *testing.B) {
	benchmarkAffectedDrugOnly(b, 50_000)
}

func benchmarkAffectedDrugOnly(b *testing.B, historical int) {
	s, _ := buildBenchmarkSystem(b, historical)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		suffix := strconv.Itoa(i)
		hotID := "hot-" + suffix
		if err := s.RegisterDrug(hotID, DrugConfig{PackSize: 1, Splittable: true}); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 20; j++ {
			id := "debt-" + suffix + "-" + string(rune('a'+j))
			input := PrescriptionInput{PatientID: "patient", IssuedAt: 100000, Lines: []LineInput{{DrugID: hotID, Quantity: 1}}}
			if err := s.AcceptPrescription(id, input, 100000); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		now := 100_000
		if err := s.ReceiveDrug(hotID, now, 1); err != nil {
			b.Fatal(err)
		}
		report, err := s.Drug(hotID, now)
		if err != nil || report.OutstandingDebt < 0 {
			b.Fatalf("report=%+v err=%v", report, err)
		}
	}
}
