
package fanout

import "testing"

func TestMatchesPrefixNotSubstring(t *testing.T) {
	cases := []struct {
		prefix string
		entity string
		want   bool
	}{
		{"user", "user", true},
		{"user", "user/1", true},
		{"user", "user.1", true},
		{"user", "superuser", false},
		{"user", "superuser/1", false},
		{"user", "users", false},
		{"user", "use", false},
		{"", "anything", true},
		{"a.b", "a.b.c", true},
		{"a.b", "a.b", true},
		{"a.b", "a.bx", false},
	}
	for _, c := range cases {
		if got := matchesPrefix(c.prefix, c.entity); got != c.want {
			t.Errorf("matchesPrefix(%q,%q)=%v want %v", c.prefix, c.entity, got, c.want)
		}
	}
}

func TestSubscribeMatching(t *testing.T) {
	d := New()
	defer d.Close()

	sAll, err := d.Subscribe("user", nil, Options{Buffer: 10})
	if err != nil {
		t.Fatal(err)
	}
	sName, err := d.Subscribe("user", []string{"name"}, Options{Buffer: 10})
	if err != nil {
		t.Fatal(err)
	}
	sOther, err := d.Subscribe("order", []string{"id", "name"}, Options{Buffer: 10})
	if err != nil {
		t.Fatal(err)
	}

	type tc struct {
		ch   Change
		want []uint64
	}
	tests := []tc{
		{Change{Entity: "user", Attr: "name"}, []uint64{sAll.ID(), sName.ID()}},
		{Change{Entity: "user", Attr: "age"}, []uint64{sAll.ID()}},
		{Change{Entity: "superuser", Attr: "name"}, nil},
		{Change{Entity: "order", Attr: "name"}, []uint64{sOther.ID()}},
		{Change{Entity: "order", Attr: "id"}, []uint64{sOther.ID()}},
		{Change{Entity: "order", Attr: "price"}, nil},
		{Change{Entity: "order/x", Attr: "id"}, []uint64{sOther.ID()}},
	}
	for i, c := range tests {
		got := d.SubscribersFor(c.ch)
		if !equal(got, c.want) {
			t.Errorf("case %d %+v: got %v want %v", i, c.ch, got, c.want)
		}
	}

	// Stability across repeated queries.
	q := d.SubscribersFor(Change{Entity: "user", Attr: "name"})
	q2 := d.SubscribersFor(Change{Entity: "user", Attr: "name"})
	if !equal(q, q2) {
		t.Fatalf("unstable query: %v vs %v", q, q2)
	}

	// End-to-end: only matching subscribers actually receive.
	if _, err := d.Publish(Change{Entity: "user", Attr: "age"}); err != nil {
		t.Fatal(err)
	}
	if m := <-sAll.C(); m.Attr != "age" {
		t.Fatalf("sAll got %q", m.Attr)
	}
	select {
	case m := <-sName.C():
		t.Fatalf("sName should not receive age: %v", m)
	default:
	}
}

func TestInvalidOptions(t *testing.T) {
	d := New()
	defer d.Close()
	if _, err := d.Subscribe("", nil, Options{Buffer: 0}); err == nil {
		t.Fatal("zero buffer must fail")
	}
	if _, err := d.Subscribe("", nil, Options{Buffer: 1, OnFull: DropPolicy(99)}); err == nil {
		t.Fatal("invalid drop policy must fail")
	}
	if _, err := d.Subscribe("", nil, Options{Buffer: 1, OnCancel: CancelPolicy(99)}); err == nil {
		t.Fatal("invalid cancel policy must fail")
	}
}
