package ontology

import (
	"errors"
	"strings"
	"testing"
)

func TestAccessorsAndErrors(t *testing.T) {
	b := &Batch{Arrival: 10, Duration: 5, Quantity: 8, Remaining: 3}
	if b.ExpiredAt() != 15 {
		t.Fatalf("ExpiredAt = %d, want 15", b.ExpiredAt())
	}
	if b.ExpiredAtTime(14) || !b.ExpiredAtTime(15) {
		t.Fatal("expiry boundary wrong")
	}
	l := SettlementLine{Quantity: 4, UnitPrice: 7, Reversed: 2}
	if l.Amount() != 28 || l.ReversedAmount() != 14 {
		t.Fatalf("amounts = %d/%d", l.Amount(), l.ReversedAmount())
	}
	e := newErr(KindOverCap, "boom")
	if !strings.Contains(e.Error(), "over_cap") || !strings.Contains(e.Error(), "boom") {
		t.Fatalf("error text = %q", e.Error())
	}
	var err error = e
	if !errors.Is(err, err) {
		t.Fatal("errors.Is sanity")
	}
	if !IsError(e, KindOverCap) || IsError(e, KindNotFound) {
		t.Fatal("IsError wrong for *Error")
	}
	if IsError(errors.New("plain"), KindOverCap) {
		t.Fatal("IsError must be false for non-ontology error")
	}
	if IsError(nil, KindOverCap) {
		t.Fatal("IsError must be false for nil")
	}
}

func TestStatementFields(t *testing.T) {
	st := Statement{Supplier: 2, Start: 0, End: 10, Consumed: 30, Reversed: 12}
	if st.Net != 0 {
		t.Fatalf("Net not zero by default; computed in Statement()")
	}
	s := NewSystem()
	_ = s.SetLimit(0, 2, 5, 100)
	_ = s.RegisterPrice(0, 2, 5, 0, 100, 10)
	_, _ = s.Receive(0, 2, 5, 10, 100)
	r, err := s.Consume(1, 5, 3)
	mustOK(t, err, "consume")
	_ = s.Reverse(2, r.Lines[0].ID, 1)
	_ = s.SetLimit(3, 2, 5, 100) // 推进时钟到 3；对账单只读不推进时钟
	out, err := s.Statement(2, 0, 3)
	mustOK(t, err, "statement")
	if out.Consumed != 30 || out.Reversed != 10 || out.Net != 20 {
		t.Fatalf("statement = %+v", out)
	}
}
