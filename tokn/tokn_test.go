package tokn

import (
	"fmt"
	"testing"
)

// TestLookupCounter pins O(1) lookup: after one domain holds m distinct
// values, an event touching one existing value and one new value inspects a
// constant number of entries regardless of m. The probe counter is
// unexported; this white-box test is the only place that reads it, and it
// never goes through an exported accessor.
func TestLookupCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			tab := NewTables(m + 1)
			values := make([]string, 0, m)
			err := tab.WithTx(func(tx *Tx) error {
				for i := 0; i < m; i++ {
					v := fmt.Sprintf("v%05d", i)
					values = append(values, v)
					if _, e := tx.Get("d", v); e != nil {
						return e
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			// One event: one existing value (hit) and one new value (alloc).
			err = tab.WithTx(func(tx *Tx) error {
				if _, e := tx.Get("d", values[0]); e != nil {
					return e
				}
				_, e := tx.Get("d", "brand-new")
				return e
			})
			if err != nil {
				t.Fatalf("target event: %v", err)
			}
			// Two Gets must be exactly two direct map probes for every m.
			if tab.lookups != 2 {
				t.Errorf("lookups=%d want 2; must not grow with m=%d", tab.lookups, m)
			}
			if tab.Size() != m+1 {
				t.Errorf("size=%d want %d", tab.Size(), m+1)
			}
		})
	}
}

// TestTxRollback verifies event-level undo: a transaction that allocates and
// then hits the cap leaves no entries behind and numbering stays gap-free.
func TestTxRollback(t *testing.T) {
	tab := NewTables(2)
	run := func(fn func(*Tx) error) error { return tab.WithTx(fn) }
	if e := run(func(tx *Tx) error {
		_, err := tx.Get("d", "a")
		return err
	}); e != nil {
		t.Fatal(e)
	}
	e := run(func(tx *Tx) error {
		if _, err := tx.Get("d", "b"); err != nil {
			return err
		}
		_, err := tx.Get("d", "c") // total would become 3 > cap 2
		return err
	})
	if e != ErrTokenLimit {
		t.Fatalf("err=%v want ErrTokenLimit", e)
	}
	if tab.Size() != 1 {
		t.Fatalf("size=%d want 1 after rollback", tab.Size())
	}
	if e := run(func(tx *Tx) error {
		tok, err := tx.Get("d", "b")
		if err != nil {
			return err
		}
		if tok != "d#2" {
			t.Errorf("b token=%q want d#2 (gap-free after rollback)", tok)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if tab.Size() != 2 {
		t.Errorf("size=%d want 2", tab.Size())
	}
}
