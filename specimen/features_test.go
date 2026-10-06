package specimen

import "testing"

func mustApply(t *testing.T, sys *System, now int64, patient string, projects ...string) (ApplicationResult, []*item) {
	t.Helper()
	result, err := sys.Apply(now, patient, projects, "P")
	if err != nil {
		t.Fatalf("Apply(%v): %v", projects, err)
	}
	items := make([]*item, 0, len(result.ItemIDs))
	for _, id := range result.ItemIDs {
		items = append(items, sys.itemsByID[id])
	}
	return result, items
}

func assertError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", code)
	}
	specimenError, ok := err.(*Error)
	if !ok || specimenError.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func setupProject(t *testing.T, sys *System, project string, max int64, cold bool, hemolysis int) {
	t.Helper()
	if err := sys.UpsertCatalogItem(0, project, CatalogRequirement{TubeType: "tube", MaxDeliverySeconds: max, ColdRequired: cold, HemolysisTolerance: hemolysis}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryBoundary(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "exact", 10, false, 0)
	setupProject(t, sys, "late", 10, false, 0)
	_, exactItems := mustApply(t, sys, 10, "p1", "exact")
	_, lateItems := mustApply(t, sys, 10, "p2", "late")

	exactTube, err := sys.Collect(10, "p1", "tube", []string{exactItems[0].id}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Dispatch(10, exactTube, TransportAmbient); err != nil {
		t.Fatal(err)
	}
	decisions, err := sys.Sign(20, exactTube, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !decisions[0].Accepted {
		t.Fatalf("equal deadline should pass: %+v", decisions)
	}

	lateTube, err := sys.Collect(20, "p2", "tube", []string{lateItems[0].id}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Dispatch(20, lateTube, TransportAmbient); err != nil {
		t.Fatal(err)
	}
	decisions, err = sys.Sign(31, lateTube, 0)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Accepted || decisions[0].RejectionReason != ReasonTimeout {
		t.Fatalf("one second late should be timeout: %+v", decisions)
	}
}

func TestHemolysisAndColdChainIndependent(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "cold", 100, true, 2)
	setupProject(t, sys, "ambient", 100, false, 1)
	_, items := mustApply(t, sys, 0, "p1", "cold", "ambient")
	tube, err := sys.Collect(0, "p1", "tube", []string{items[0].id, items[1].id}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Dispatch(0, tube, TransportCold); err != nil {
		t.Fatal(err)
	}
	decisions, err := sys.Sign(1, tube, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !decisions[0].Accepted || !decisions[1].Accepted {
		t.Fatalf("hemolysis equal tolerance should pass: %+v", decisions)
	}

	_, items2 := mustApply(t, sys, 2, "p2", "cold", "ambient")
	tube2, err := sys.Collect(2, "p2", "tube", []string{items2[0].id, items2[1].id}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Dispatch(2, tube2, TransportAmbient); err != nil {
		t.Fatal(err)
	}
	decisions, _ = sys.Sign(3, tube2, 0)
	if decisions[0].Accepted || decisions[0].RejectionReason != ReasonColdChain || !decisions[1].Accepted {
		t.Fatalf("only cold-required item should reject ambient transport: %+v", decisions)
	}

	_, items3 := mustApply(t, sys, 4, "p3", "cold", "ambient")
	tube3, _ := sys.Collect(4, "p3", "tube", []string{items3[0].id, items3[1].id}, 4)
	_ = sys.Dispatch(4, tube3, TransportCold)
	decisions, _ = sys.Sign(5, tube3, 2)
	if !decisions[0].Accepted || decisions[1].Accepted || decisions[1].RejectionReason != ReasonHemolysis {
		t.Fatalf("both should independently evaluate hemolysis: %+v", decisions)
	}
}

func TestThirdRejectionTerminates(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	_, items := mustApply(t, sys, 10, "p1", "x")
	for count := 1; count <= 3; count++ {
		collectAt := int64(count * 20)
		tube, err := sys.Collect(collectAt, "p1", "tube", []string{items[0].id}, collectAt)
		if err != nil {
			t.Fatal(err)
		}
		_ = sys.Dispatch(collectAt, tube, TransportAmbient)
		decisions, err := sys.Sign(collectAt+11, tube, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantStatus := StatusWaiting
		if count == 3 {
			wantStatus = StatusTerminated
		}
		if sys.itemsByID[decisions[0].ItemID].status != wantStatus || sys.itemsByID[decisions[0].ItemID].rejectionCount != count {
			t.Fatalf("rejection %d state wrong: %+v", count, decisions)
		}
	}
	view, err := sys.Query(72, "p1")
	if err != nil || len(view.Items) != 0 {
		t.Fatalf("terminated item must leave active set: %+v %v", view, err)
	}
}

func TestRecollectionMergesAcrossApplications(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	setupProject(t, sys, "y", 10, false, 0)
	first, xItems := mustApply(t, sys, 0, "p1", "x")
	second, yItems := mustApply(t, sys, 0, "p1", "y")
	tube1, _ := sys.Collect(0, "p1", "tube", []string{xItems[0].id}, 0)
	_ = sys.Dispatch(0, tube1, TransportAmbient)
	_, _ = sys.Sign(11, tube1, 0)

	merged, err := sys.Collect(12, "p1", "tube", []string{xItems[0].id, yItems[0].id}, 12)
	if err != nil {
		t.Fatal(err)
	}
	_ = sys.Dispatch(12, merged, TransportAmbient)
	decisions, err := sys.Sign(22, merged, 0)
	if err != nil || len(decisions) != 2 {
		t.Fatalf("recollection should merge with another application: %+v %v", decisions, err)
	}
	x := sys.itemsByID[xItems[0].id]
	y := sys.itemsByID[yItems[0].id]
	if x.applicationID != first.ApplicationID || y.applicationID != second.ApplicationID || x.priority != "P" {
		t.Fatalf("recollection must preserve original application and priority")
	}
}

func TestDuplicateApplicationRejectedAtomically(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	setupProject(t, sys, "y", 10, false, 0)
	_, _ = mustApply(t, sys, 0, "p1", "x")
	beforeCount := len(sys.itemsByID)
	_, err := sys.Apply(1, "p1", []string{"y", "x"}, "P")
	assertError(t, err, CodeDuplicateRequest)
	if len(sys.itemsByID) != beforeCount || sys.appSequence != 1 || sys.itemSequence != 1 || sys.Now() != 0 {
		t.Fatalf("rejected application had side effects: apps=%d items=%d now=%d", sys.appSequence, len(sys.itemsByID), sys.Now())
	}
}

func TestCancellationVoidsTube(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	_, items := mustApply(t, sys, 0, "p1", "x")
	tube, _ := sys.Collect(0, "p1", "tube", []string{items[0].id}, 0)
	_ = sys.Dispatch(0, tube, TransportAmbient)
	if err := sys.Cancel(1, items[0].id); err != nil {
		t.Fatal(err)
	}
	_, err := sys.Sign(2, tube, 0)
	assertError(t, err, CodeStatusMismatch)
	err = sys.Cancel(2, items[0].id)
	assertError(t, err, CodeStatusMismatch)
}

func TestCatalogChangeNotRetroactive(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	_, items := mustApply(t, sys, 0, "p1", "x")
	if err := sys.UpsertCatalogItem(1, "x", CatalogRequirement{TubeType: "tube", MaxDeliverySeconds: 5, ColdRequired: false, HemolysisTolerance: 0}); err != nil {
		t.Fatal(err)
	}
	tube, _ := sys.Collect(1, "p1", "tube", []string{items[0].id}, 1)
	_ = sys.Dispatch(1, tube, TransportAmbient)
	decisions, err := sys.Sign(11, tube, 0)
	if err != nil || !decisions[0].Accepted {
		t.Fatalf("submitted application must use submission snapshot: %+v %v", decisions, err)
	}
}

func TestDispatchLastValueWins(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, true, 0)
	_, items := mustApply(t, sys, 0, "p1", "x")
	tube, _ := sys.Collect(0, "p1", "tube", []string{items[0].id}, 0)
	_ = sys.Dispatch(0, tube, TransportAmbient)
	_ = sys.Dispatch(1, tube, TransportCold)
	decisions, err := sys.Sign(2, tube, 0)
	if err != nil || !decisions[0].Accepted {
		t.Fatalf("last dispatch method should be used: %+v %v", decisions, err)
	}
}

func TestErrorPriority(t *testing.T) {
	sys := NewSystem()
	setupProject(t, sys, "x", 10, false, 0)
	_, items := mustApply(t, sys, 10, "p1", "x")
	_, err := sys.Apply(-1, "p1", []string{"x"}, "")
	assertError(t, err, CodeInvalidParameter)
	_, err = sys.Apply(9, "p1", []string{"x"}, "P")
	assertError(t, err, CodeClockRollback)
	_, err = sys.Apply(10, "p1", []string{"missing"}, "P")
	assertError(t, err, CodeNotFound)
	_, err = sys.Apply(10, "p1", []string{"x"}, "P")
	assertError(t, err, CodeDuplicateRequest)

	tube, _ := sys.Collect(11, "p1", "wrong", []string{items[0].id}, 11)
	if tube != "" {
		t.Fatal("tube mismatch should reject")
	}
	if err := sys.Cancel(12, items[0].id); err != nil {
		t.Fatal(err)
	}
	_, err = sys.Collect(13, "p1", "tube", []string{items[0].id}, 13)
	assertError(t, err, CodeStatusMismatch)
	_, err = sys.Collect(14, "p1", "tube", []string{"missing"}, 14)
	assertError(t, err, CodeNotFound)
}
