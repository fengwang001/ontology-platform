package loan

import "testing"

func TestExampleOneWithoutChanges(t *testing.T) {
	l, err := New(100000, 10000, 30000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := l.Pay()
	if err != nil {
		t.Fatalf("Pay() error = %v", err)
	}
	wantFirst := Payment{Period: 1, Amount: 30000, Interest: 1000, Principal: 29000, Balance: 71000}
	if first != wantFirst {
		t.Fatalf("first payment = %+v, want %+v", first, wantFirst)
	}
	if remaining := l.Remaining(); remaining != 3 {
		t.Fatalf("Remaining() = %d, want 3", remaining)
	}

	assertPay(t, l, Payment{Period: 2, Amount: 30000, Interest: 710, Principal: 29290, Balance: 41710})
	assertPay(t, l, Payment{Period: 3, Amount: 30000, Interest: 417, Principal: 29583, Balance: 12127})
	assertPay(t, l, Payment{Period: 4, Amount: 12248, Interest: 121, Principal: 12127, Balance: 0})
	if remaining := l.Remaining(); remaining != 0 {
		t.Fatalf("Remaining() after payoff = %d, want 0", remaining)
	}
}

func TestExampleOneWithPrepayment(t *testing.T) {
	l, err := New(100000, 10000, 30000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := l.Pay(); err != nil {
		t.Fatalf("Pay() error = %v", err)
	}

	prepayment, err := l.Prepay(30000)
	if err != nil {
		t.Fatalf("Prepay() error = %v", err)
	}
	wantPrepayment := Prepayment{Amount: 30000, Fee: 900, Balance: 41000}
	if prepayment != wantPrepayment {
		t.Fatalf("prepayment = %+v, want %+v", prepayment, wantPrepayment)
	}
	if remaining := l.Remaining(); remaining != 2 {
		t.Fatalf("Remaining() = %d, want 2", remaining)
	}

	assertPay(t, l, Payment{Period: 2, Amount: 30000, Interest: 410, Principal: 29590, Balance: 11410})
	assertPay(t, l, Payment{Period: 3, Amount: 11524, Interest: 114, Principal: 11410, Balance: 0})
}

func TestExampleTwoRateChangeDuringHoliday(t *testing.T) {
	l, err := New(100000, 10000, 30000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := l.Pay(); err != nil {
		t.Fatalf("Pay() error = %v", err)
	}
	if err := l.SetRate(20000, 3); err != nil {
		t.Fatalf("SetRate() error = %v", err)
	}
	if err := l.Holiday(2); err != nil {
		t.Fatalf("Holiday() error = %v", err)
	}
	if remaining := l.Remaining(); remaining != 5 {
		t.Fatalf("Remaining() after Holiday() = %d, want 5", remaining)
	}

	assertPay(t, l, Payment{Period: 2, Amount: 710, Interest: 710, Balance: 71000})
	assertPay(t, l, Payment{Period: 3, Amount: 1420, Interest: 1420, Balance: 71000})
	assertPay(t, l, Payment{Period: 4, Amount: 30000, Interest: 1420, Principal: 28580, Balance: 42420})
	assertPay(t, l, Payment{Period: 5, Amount: 30000, Interest: 848, Principal: 29152, Balance: 13268})
	assertPay(t, l, Payment{Period: 6, Amount: 13533, Interest: 265, Principal: 13268, Balance: 0})
}

func assertPay(t *testing.T, l *Loan, want Payment) {
	t.Helper()
	got, err := l.Pay()
	if err != nil {
		t.Fatalf("Pay() period %d error = %v", want.Period, err)
	}
	if got != want {
		t.Fatalf("Pay() period %d = %+v, want %+v", want.Period, got, want)
	}
}
