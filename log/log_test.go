package log

import (
	"slices"
	"testing"

	"ontology/ent"
)

// TestAppendConstantWork proves Append consults only the cached chain head:
// the number of existing entries read for the predecessor hash must not
// grow with the log length m.
func TestAppendConstantWork(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		l := New()
		for i := 0; i < m; i++ {
			if _, err := l.Append(int64(i), "u", "op"); err != nil {
				t.Fatalf("m=%d append %d: %v", m, i, err)
			}
		}
		if _, err := l.Append(int64(m), "u", "op"); err != nil {
			t.Fatalf("m=%d final append: %v", m, err)
		}
		if l.lastRead > 1 {
			t.Fatalf("m=%d: last Append read %d entries, want <= 1", m, l.lastRead)
		}
	}
}

// TestVerifyDetectsTamper pins invariant 2: Verify returns exactly the Seq
// of the first tampered entry (the minimum when several are tampered).
func TestVerifyDetectsTamper(t *testing.T) {
	const n = 8
	build := func() *Log {
		l := New()
		for i := 1; i <= n; i++ {
			if _, err := l.Append(int64(i*10), "u", "op"); err != nil {
				t.Fatal(err)
			}
		}
		return l
	}
	if v := build().Verify(); v != -1 {
		t.Fatalf("clean log: Verify=%d, want -1", v)
	}
	for i := 0; i <= n; i++ { // tamper each single entry in turn
		l := build()
		l.entries[i].Who = "evil"
		if v := l.Verify(); v != int64(i) {
			t.Fatalf("tamper entry %d: Verify=%d, want %d", i, v, i)
		}
	}
	l := build() // tamper several: smallest Seq wins
	l.entries[5].Who = "x"
	l.entries[2].Who = "y"
	l.entries[7].Op = "z"
	if v := l.Verify(); v != 2 {
		t.Fatalf("multi-tamper: Verify=%d, want 2", v)
	}
	if got := l.Affected(2); len(got) != n-1 { // Seq 2..8
		t.Fatalf("Affected(2)=%v, want 7 entries", got)
	}
}

// TestHashChainConsistent pins invariant 3: every prefix recomputes cleanly.
func TestHashChainConsistent(t *testing.T) {
	for _, n := range []int{1, 7, 100, 1000} {
		l := New()
		ts := int64(0)
		for i := 0; i < n; i++ { // deterministic non-decreasing timestamps
			ts += int64((i*7)%5 + 1)
			if _, err := l.Append(ts, "u", "op"); err != nil {
				t.Fatal(err)
			}
		}
		prev := ent.GenesisHash()
		for k, e := range l.Entries() {
			if k == 0 && e != ent.Genesis() {
				t.Fatalf("n=%d: genesis mutated", n)
			}
			if k > 0 {
				if want := ent.ComputeHash(prev, e.Seq, e.TS, e.Who, e.Op); e.Hash != want {
					t.Fatalf("n=%d: prefix %d hash mismatch", n, k)
				}
				prev = e.Hash
			}
		}
		if v := l.Verify(); v != -1 {
			t.Fatalf("n=%d: Verify=%d, want -1", n, v)
		}
	}
}

// TestAffected: tampering Seq=3 poisons the continuous suffix [3,4,5].
func TestAffected(t *testing.T) {
	l := New()
	for _, ts := range []int64{10, 12, 12, 15, 20} {
		if _, err := l.Append(ts, "u", "op"); err != nil {
			t.Fatal(err)
		}
	}
	es := l.Entries()
	es[3].Op = "put"
	tampered := NewFrom(es)
	if v := tampered.Verify(); v != 3 {
		t.Fatalf("Verify=%d, want 3", v)
	}
	if got := tampered.Affected(3); !slices.Equal(got, []int64{3, 4, 5}) {
		t.Fatalf("Affected(3)=%v, want [3 4 5]", got)
	}
	if got := l.Affected(99); len(got) != 0 {
		t.Fatalf("Affected(99)=%v, want empty", got)
	}
	if got := l.Affected(0); len(got) != len(l.Entries()) {
		t.Fatalf("Affected(0)=%v, want all entries", got)
	}
}
