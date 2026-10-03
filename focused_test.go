package ontology

import (
	"errors"
	"testing"
)

func testMachine(t *testing.T) *StateMachine {
	t.Helper()
	machine, err := New(Config{
		AuthPendingTTL:   100,
		AuthValidTTL:     1000,
		OrderTTL:         500,
		FailureWindow:    60,
		FailureThreshold: 2,
		NonceCapacity:    3,
		PendingAuthLimit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func assertErrorKind(t *testing.T, err error, kind string) {
	t.Helper()
	var ontologyError *Error
	if !errors.As(err, &ontologyError) || ontologyError.Kind != kind {
		t.Fatalf("got error %v, want kind %s", err, kind)
	}
}

func assertStatus(t *testing.T, machine *StateMachine, orderID string, now int64, want string) {
	t.Helper()
	result, err := machine.Status(orderID, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != want {
		t.Fatalf("%s at %d = %s, want %s", orderID, now, result.Status, want)
	}
}

func TestNoncePoolEvictsSmallestAndConsumesAccepted(t *testing.T) {
	machine := testMachine(t)
	nonces := []uint64{machine.Nonce(), machine.Nonce(), machine.Nonce(), machine.Nonce()}
	want := []uint64{1, 2, 3, 4}
	for i := range want {
		if nonces[i] != want[i] {
			t.Fatalf("nonce %d = %d", i, nonces[i])
		}
	}

	_, err := machine.NewOrder([]byte("acc"), []string{"a.com"}, 1, 0)
	assertErrorKind(t, err, KindBadNonce)

	order, err := machine.NewOrder([]byte("acc"), []string{"a.com"}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "o1" || order.Expires != 500 || len(order.AuthorizationIDs) != 1 || order.AuthorizationIDs[0] != "z1" {
		t.Fatalf("unexpected order: %+v", order)
	}

	_, err = machine.NewOrder([]byte("acc"), []string{"a.com"}, 2, 1)
	assertErrorKind(t, err, KindBadNonce)

	auth, effective, err := machine.Authorization("z1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != StatusPending || effective != StatusPending || auth.Expires != 100 {
		t.Fatalf("unexpected auth: %+v effective=%s", auth, effective)
	}

	report, err := machine.Report("z1", true, 10)
	if err != nil || report.Status != StatusValid || report.Expires != 1010 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	assertStatus(t, machine, "o1", 10, StatusReady)

	if _, err := machine.Finalize([]byte("acc"), "o1", []string{"a.com"}, 3, 10); err != nil {
		t.Fatal(err)
	}
	_, err = machine.Finalize([]byte("acc"), "o1", []string{"a.com"}, 4, 30)
	assertErrorKind(t, err, KindConflict)

	if machine.nonceOrder.Len() != 1 {
		t.Fatalf("rejected operation consumed nonce: pool len %d", machine.nonceOrder.Len())
	}
}

func TestOrderStatusFiveWayDerivation(t *testing.T) {
	machine := testMachine(t)
	invalidAuthOrder, err := machine.NewOrder([]byte("acc-invalid"), []string{"a.com", "b.com"}, machine.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, invalidAuthOrder.ID, 10, StatusPending)
	if _, err := machine.Report("z2", false, 10); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, invalidAuthOrder.ID, 10, StatusInvalid)

	deactivatedOrder, err := machine.NewOrder([]byte("acc-deactivated"), []string{"c.com"}, machine.Nonce(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z3", true, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Deactivate([]byte("acc-deactivated"), "z3", machine.Nonce(), 21); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, deactivatedOrder.ID, 21, StatusInvalid)

	expiredAuthOrder, err := machine.NewOrder([]byte("acc-expired-auth"), []string{"d.com"}, machine.Nonce(), 30)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, expiredAuthOrder.ID, 129, StatusPending)
	assertStatus(t, machine, expiredAuthOrder.ID, 130, StatusInvalid)

	readyOrder, err := machine.NewOrder([]byte("acc-ready"), []string{"e.com", "f.com"}, machine.Nonce(), 40)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, readyOrder.ID, 40, StatusPending)
	if _, err := machine.Report("z5", true, 40); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, readyOrder.ID, 40, StatusPending)
	if _, err := machine.Report("z6", true, 40); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, readyOrder.ID, 40, StatusReady)
	assertStatus(t, machine, readyOrder.ID, 539, StatusReady)
	assertStatus(t, machine, readyOrder.ID, 540, StatusInvalid)

	validOrder, err := machine.NewOrder([]byte("acc-valid"), []string{"g.com"}, machine.Nonce(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Report("z7", true, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Finalize([]byte("acc-valid"), validOrder.ID, []string{"g.com"}, machine.Nonce(), 50); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, machine, validOrder.ID, 1050, StatusValid)

	stored, effective, err := machine.Authorization("z7", 1050)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusValid || effective != StatusExpired {
		t.Fatalf("stored=%s effective=%s", stored.Status, effective)
	}
}
