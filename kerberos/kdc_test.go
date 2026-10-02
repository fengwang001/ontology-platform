package kerberos

import "testing"

func TestSpecExample(t *testing.T) {
	k, err := NewKDC(100, 1000, 5, 50)
	if err != nil {
		t.Fatal(err)
	}

	t1, err := k.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, t1, 10, 110, 1010, false)

	t1, err = k.Renew(t1.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, t1, 100, 200, 1010, false)

	t2, err := k.TGS(t1.ID, []byte("web"), 1000, 1500, 150)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, t2, 150, 200, 1010, false)

	if _, err := k.Authenticate(t2.ID, 148, 150); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(t2.ID, 148, 152); err == nil || err.(*Error).Reason != ErrReplay {
		t.Fatalf("expected replay, got %v", err)
	}
	if _, err := k.Authenticate(t2.ID, 148, 153); err == nil || err.(*Error).Reason != ErrReplay {
		t.Fatalf("expected replay, got %v", err)
	}
	if _, err := k.Authenticate(t2.ID, 148, 154); err == nil || err.(*Error).Reason != ErrClockSkew {
		t.Fatalf("expected clock skew, got %v", err)
	}
	if _, err := k.Authenticate(t2.ID, 149, 152); err != nil {
		t.Fatal(err)
	}

	t2, err = k.Renew(t2.ID, 160)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, t2, 160, 210, 1010, false)
}

func assertTicket(t *testing.T, ticket *Ticket, start, end, renewTill int64, invalid bool) {
	t.Helper()
	if ticket.Start != start || ticket.End != end || ticket.RenewTill != renewTill || ticket.Invalid != invalid {
		t.Fatalf("ticket = [%d,%d) rt=%d invalid=%v; want [%d,%d) rt=%d invalid=%v",
			ticket.Start, ticket.End, ticket.RenewTill, ticket.Invalid,
			start, end, renewTill, invalid)
	}
}
