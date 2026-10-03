package ontology

import "testing"

func TestReuseSelectionAndSharedDeactivation(t *testing.T) {
	machine := testMachine(t)
	first, err := machine.NewOrder([]byte("acc"), []string{"a.com", "b.com"}, machine.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z2", true, 10); err != nil {
		t.Fatal(err)
	}

	second, err := machine.NewOrder([]byte("acc"), []string{"a.com"}, machine.Nonce(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if second.AuthorizationIDs[0] != "z1" || second.Expires != 520 {
		t.Fatalf("did not reuse z1: %+v", second)
	}

	third, err := machine.NewOrder([]byte("acc"), []string{"a.com", "c.com"}, machine.Nonce(), 450)
	if err != nil {
		t.Fatal(err)
	}
	if third.AuthorizationIDs[0] != "z1" || third.AuthorizationIDs[1] != "z3" {
		t.Fatalf("unexpected reuse/shared mapping: %+v", third)
	}
	assertStatus(t, machine, first.ID, 450, StatusReady)
	assertStatus(t, machine, second.ID, 450, StatusReady)
	assertStatus(t, machine, third.ID, 450, StatusPending)

	if _, err := machine.Deactivate([]byte("acc"), "z1", machine.Nonce(), 460); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, first.ID, 460, StatusInvalid)
	assertStatus(t, machine, second.ID, 460, StatusInvalid)
	assertStatus(t, machine, third.ID, 460, StatusInvalid)
}

func TestFinalizeCSRSetOrderAndMismatchDoesNotConsumeNonce(t *testing.T) {
	machine := testMachine(t)
	order, err := machine.NewOrder([]byte("acc"), []string{"a.com", "b.com"}, machine.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z1", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z2", true, 0); err != nil {
		t.Fatal(err)
	}

	nonce := machine.Nonce()
	_, err = machine.Finalize([]byte("acc"), order.ID, []string{"a.com"}, nonce, 1)
	assertErrorKind(t, err, KindCSRMismatch)
	_, err = machine.Finalize([]byte("acc"), order.ID, []string{"a.com", "c.com"}, nonce, 1)
	assertErrorKind(t, err, KindCSRMismatch)

	finalized, err := machine.Finalize([]byte("acc"), order.ID, []string{"b.com", "a.com"}, nonce, 1)
	if err != nil {
		t.Fatal(err)
	}
	if finalized.Status != StatusValid || finalized.CertSerial != 1 {
		t.Fatalf("unexpected finalize result: %+v", finalized)
	}
}

func TestSlidingFailureWindowAndNoPartialCreation(t *testing.T) {
	machine := testMachine(t)
	first, err := machine.NewOrder([]byte("acc"), []string{"c.com"}, machine.Nonce(), 40)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report(first.AuthorizationIDs[0], false, 100); err != nil {
		t.Fatal(err)
	}

	second, err := machine.NewOrder([]byte("acc"), []string{"c.com"}, machine.Nonce(), 101)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report(second.AuthorizationIDs[0], false, 130); err != nil {
		t.Fatal(err)
	}

	beforeOrders := len(machine.orders)
	beforeAuths := len(machine.auths)
	beforeNonces := machine.nonceOrder.Len()
	nonce := machine.Nonce()
	_, err = machine.NewOrder([]byte("acc"), []string{"c.com", "d.com"}, nonce, 159)
	assertErrorKind(t, err, KindRateLimited)
	var ontologyError *Error
	if !asOntologyError(err, &ontologyError) || ontologyError.Identifier != "c.com" || ontologyError.Count != 2 {
		t.Fatalf("unexpected rate-limit details: %+v", err)
	}
	if len(machine.orders) != beforeOrders || len(machine.auths) != beforeAuths || machine.nonceOrder.Len() != beforeNonces+1 {
		t.Fatal("rejected rate-limited order had partial effects")
	}

	accepted, err := machine.NewOrder([]byte("acc"), []string{"c.com", "d.com"}, nonce, 160)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.AuthorizationIDs[0] != "z3" || accepted.AuthorizationIDs[1] != "z4" {
		t.Fatalf("unexpected reuse after window edge: %+v", accepted)
	}
}

func TestQuotaBoundariesReuseAndRelease(t *testing.T) {
	machine := testMachine(t)
	_, err := machine.NewOrder([]byte("acc"), []string{"a.com", "b.com", "c.com"}, machine.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}

	nonce := machine.Nonce()
	_, err = machine.NewOrder([]byte("acc"), []string{"d.com"}, nonce, 99)
	assertErrorKind(t, err, KindQuotaExceeded)
	var ontologyError *Error
	if !asOntologyError(err, &ontologyError) || ontologyError.P != 3 || ontologyError.Q != 1 {
		t.Fatalf("unexpected quota details: %+v", err)
	}

	if _, err := machine.Report("z1", true, 99); err != nil {
		t.Fatal(err)
	}
	order, err := machine.NewOrder([]byte("acc"), []string{"a.com", "d.com"}, nonce, 99)
	if err != nil {
		t.Fatal(err)
	}
	if order.AuthorizationIDs[0] != "z1" || order.AuthorizationIDs[1] != "z4" {
		t.Fatalf("reuse should not occupy quota: %+v", order)
	}

	other, err := machine.NewOrder([]byte("other"), []string{"e.com"}, machine.Nonce(), 99)
	if err != nil {
		t.Fatal(err)
	}
	if other.AuthorizationIDs[0] != "z5" {
		t.Fatalf("accounts should have independent quota: %+v", other)
	}

	if _, err := machine.Deactivate([]byte("acc"), "z2", machine.Nonce(), 99); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.NewOrder([]byte("acc"), []string{"f.com"}, machine.Nonce(), 99); err != nil {
		t.Fatal(err)
	}

	_, err = machine.NewOrder([]byte("acc"), []string{"g.com"}, machine.Nonce(), 100)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpiredPendingReportIsConflictAndRejectionsAreAtomic(t *testing.T) {
	machine := testMachine(t)
	order, err := machine.NewOrder([]byte("acc"), []string{"a.com"}, machine.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = machine.Report(order.AuthorizationIDs[0], true, 100)
	assertErrorKind(t, err, KindConflict)
	var ontologyError *Error
	if !asOntologyError(err, &ontologyError) || ontologyError.Current != StatusExpired {
		t.Fatalf("unexpected conflict state: %+v", err)
	}

	stored, effective, err := machine.Authorization(order.AuthorizationIDs[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusPending || effective != StatusExpired {
		t.Fatalf("rejection changed state: stored=%s effective=%s", stored.Status, effective)
	}
}

func asOntologyError(err error, target **Error) bool {
	for err != nil {
		if ontologyError, ok := err.(*Error); ok {
			*target = ontologyError
			return true
		}
		next, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}
