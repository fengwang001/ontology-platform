package ontology

import "testing"

func TestPrefixDoesNotDegradeToSubstring(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{EntityPrefix: "user", Buffer: 8, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		entity string
		want   bool
	}{
		{"user", true},
		{"user.alice", true},
		{"user/alice", true},
		{"superuser", false}, // substring only, must not match
		{"a.user", false},
		{"admin", false},
	}
	for _, c := range cases {
		if ids := d.Match(c.entity, "name"); (len(ids) == 1) != c.want {
			t.Errorf("Match(%q) matched=%v, want matched=%v", c.entity, !c.want, c.want)
		}
	}
	s.Unsubscribe()
}

func TestPropertySetMatchesExactly(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Properties: []string{"name", "email"}, Buffer: 8, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"name", "email"} {
		if ids := d.Match("e", p); len(ids) != 1 {
			t.Errorf("Match(e, %q) = %v, want one hit", p, ids)
		}
	}
	for _, p := range []string{"names", "Name", "name2", ""} {
		if ids := d.Match("e", p); len(ids) != 0 {
			t.Errorf("Match(e, %q) = %v, want no hit (exact equality only)", p, ids)
		}
	}
	s.Unsubscribe()
}

func TestEmptyPropertySetMatchesAll(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 8, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "b", "anything"} {
		if ids := d.Match("e", p); len(ids) != 1 {
			t.Errorf("Match(e, %q) = %v, want one hit for empty property set", p, ids)
		}
	}
	s.Unsubscribe()
}

func TestMatchQueryIsStableAndOrdered(t *testing.T) {
	d := New()
	defer d.Close()
	var subs []*Subscription
	for i := 0; i < 5; i++ {
		s, err := d.Subscribe(Options{EntityPrefix: "e", Buffer: 1})
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	// Remove a middle subscription; the remaining order must stay sorted.
	subs[2].Unsubscribe()
	want := []uint64{subs[0].ID(), subs[1].ID(), subs[3].ID(), subs[4].ID()}
	for i := 0; i < 3; i++ {
		got := d.Match("entity", "p")
		if len(got) != len(want) {
			t.Fatalf("Match returned %v, want %v", got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("Match returned %v, want %v", got, want)
			}
		}
	}
	for _, s := range subs {
		s.Unsubscribe()
	}
}

func TestSeqGapsEqualDroppedCount(t *testing.T) {
	d := New()
	defer d.Close()
	// Match-everything subscription: every published message is either
	// received or dropped, so the received-sequence gaps must equal the
	// drop count exactly.
	s, err := d.Subscribe(Options{Buffer: 3, OnFull: DropNewest, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	const n = 10
	for i := 0; i < n; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	s.Unsubscribe()
	got := recvAll(t, s)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("received seqs not strictly increasing: %v", got)
		}
	}
	gaps := uint64(n) - uint64(len(got))
	if gaps != s.Dropped() {
		t.Fatalf("gap count %d != Dropped() %d (received %v)", gaps, s.Dropped(), got)
	}
	if s.LastDropSeq() != n {
		t.Fatalf("LastDropSeq() = %d, want %d", s.LastDropSeq(), n)
	}
}
