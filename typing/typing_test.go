package typing

import (
	"testing"

	"ontology/bloodstock"
)

func gstr(gs []bloodstock.Group) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.ABO.String()+g.Rh.String())
	}
	return out
}

func TestGroupsFor(t *testing.T) {
	cases := []struct {
		rec  Record
		want []string
	}{
		{Record{State: Confirmed, Type: Result{bloodstock.A, bloodstock.RhPos}},
			[]string{"A阳", "A阴", "O阳", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.A, bloodstock.RhNeg}},
			[]string{"A阴", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.AB, bloodstock.RhPos}},
			[]string{"AB阳", "AB阴", "A阳", "A阴", "B阳", "B阴", "O阳", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.AB, bloodstock.RhNeg}},
			[]string{"AB阴", "A阴", "B阴", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.B, bloodstock.RhPos}},
			[]string{"B阳", "B阴", "O阳", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.O, bloodstock.RhPos}},
			[]string{"O阳", "O阴"}},
		{Record{State: Confirmed, Type: Result{bloodstock.O, bloodstock.RhNeg}},
			[]string{"O阴"}},
		{Record{State: Single, Type: Result{bloodstock.A, bloodstock.RhPos}},
			[]string{"O阳", "O阴"}},
		{Record{State: Single, Type: Result{bloodstock.B, bloodstock.RhNeg}},
			[]string{"O阴"}},
		{Record{State: Unknown}, []string{"O阴"}},
		{Record{State: Disputed}, []string{"O阴"}},
	}
	for i, c := range cases {
		if got := gstr(GroupsFor(c.rec)); eq(got, c.want) != "" {
			t.Fatalf("case %d: got %v want %v (%s)", i, got, c.want, eq(got, c.want))
		}
	}
}

func eq(a, b []string) string {
	if len(a) != len(b) {
		return "len"
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] + "!=" + b[i]
		}
	}
	return ""
}

func TestTypeStateMachine(t *testing.T) {
	b := NewBook()
	if got := b.Get("P").State; got != Unknown {
		t.Fatalf("zero = %v", got)
	}
	if d, err := b.Type("P", "s1", bloodstock.A, bloodstock.RhPos); err != nil || d {
		t.Fatalf("first: d=%v err=%v", d, err)
	}
	if b.Get("P").State != Single {
		t.Fatal("expect single")
	}
	if d, err := b.Type("P", "s2", bloodstock.A, bloodstock.RhPos); err != nil || d {
		t.Fatalf("second consistent: d=%v err=%v", d, err)
	}
	if b.Get("P").State != Confirmed {
		t.Fatal("expect confirmed")
	}
	if d, err := b.Type("P", "s3", bloodstock.B, bloodstock.RhPos); err != nil || !d {
		t.Fatalf("inconsistent: d=%v err=%v", d, err)
	}
	if b.Get("P").State != Disputed {
		t.Fatal("expect disputed")
	}
	// 粘滞：再来结果仍是存疑，disputed 不再重复报告
	if d, err := b.Type("P", "s4", bloodstock.A, bloodstock.RhPos); err != nil || d {
		t.Fatalf("sticky: d=%v err=%v", d, err)
	}
	if b.Get("P").State != Disputed {
		t.Fatal("still disputed")
	}
	if err := b.Resolve("P", bloodstock.O, bloodstock.RhNeg); err != nil {
		t.Fatal(err)
	}
	if b.Get("P").State != Confirmed {
		t.Fatal("resolve -> confirmed")
	}
	if err := b.Resolve("NOBODY", bloodstock.O, bloodstock.RhNeg); err != bloodstock.ErrNotFound {
		t.Fatalf("resolve unknown = %v", err)
	}
	if _, err := b.Type("Q", "s1", bloodstock.O, bloodstock.RhNeg); err != bloodstock.ErrDuplicate {
		t.Fatalf("dup sample = %v", err)
	}
}
