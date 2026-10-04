package review

import "testing"

// buildProbeWorld builds a catalogue of the requested size plus an extra
// patient holding an unrelated prescription, so the probe can demonstrate
// independence from catalogue size and other patients' history.
func buildProbeWorld(t *testing.T, drugCount int) (*Engine, []string, int, int) {
	t.Helper()
	e := NewEngine(10)
	if err := e.AddDoctor("d1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddPharmacist("ph"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddPatient("target"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddPatient("other"); err != nil {
		t.Fatal(err)
	}
	targetDrugs := []string{"T1", "T2", "T3"}
	for i, ing := range []string{"A", "B", "C"} {
		if err := e.Formulary().AddDrug(targetDrugs[i], []byte(ing), 100, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < drugCount; i++ {
		id := "X" + itoaProbe(i)
		if err := e.Formulary().AddDrug(id, []byte("F"+itoaProbe(i)), 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	// Two reference items for the target patient (ingredients A and B).
	r1, _, _, err := e.Submit(1, "d1", "target", []Item{
		{Drug: "T1", Per: 1, PerDay: 1, Start: 1, End: 30},
		{Drug: "T2", Per: 1, PerDay: 1, Start: 1, End: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The "other" patient holds many prescriptions; none may be queried.
	for i := 0; i < 20; i++ {
		_, _, _, err := e.Submit(1, "d1", "other", []Item{
			{Drug: "X" + itoaProbe(i), Per: 1, PerDay: 1, Start: 1, End: 30},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return e, targetDrugs, r1, drugCount
}

func itoaProbe(x int) string {
	if x == 0 {
		return "0"
	}
	var b []byte
	for x > 0 {
		b = append([]byte{byte('0' + x%10)}, b...)
		x /= 10
	}
	return string(b)
}

// TestInteractionProbe proves one Submit performs at most n*(n+r)
// interaction-table lookups, independent of catalogue size (100 vs 10000)
// and of other patients' prescription counts.
func TestInteractionProbe(t *testing.T) {
	measure := func(t *testing.T, drugCount int) int {
		t.Helper()
		e, targetDrugs, _, _ := buildProbeWorld(t, drugCount)
		// n=2 new items (B and C); r=2 reference items for this patient.
		items := []Item{
			{Drug: targetDrugs[1], Per: 1, PerDay: 1, Start: 5, End: 10},
			{Drug: targetDrugs[2], Per: 1, PerDay: 1, Start: 5, End: 10},
		}
		_, _, _, err := e.Submit(5, "d1", "target", items)
		if err != nil {
			t.Fatal(err)
		}
		q := e.interactionQueries()
		n := len(items)
		r := 2
		if q > n*(n+r) {
			t.Fatalf("drugCount=%d queries=%d exceed bound n*(n+r)=%d", drugCount, q, n*(n+r))
		}
		return q
	}
	q100 := measure(t, 100)
	q10000 := measure(t, 10000)
	if q100 != q10000 {
		t.Fatalf("queries depend on catalogue size: %d vs %d", q100, q10000)
	}
	t.Logf("interaction lookups per Submit = %d (catalogue 100 and 10000 identical)", q100)
}
