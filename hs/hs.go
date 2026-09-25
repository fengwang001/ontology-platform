// Package hs holds the passive side's half-open connection table and ISN
// negotiation. It depends on no other package in this module.
package hs

import "fmt"

// Conn is a connection's negotiated pair of initial sequence numbers.
type Conn struct {
	ClientISN int64
	ServerISN int64
}

// Table is the half-open connection table (SYN received, ACK not yet received).
type Table struct {
	half    map[string]Conn
	next    int64 // monotonic allocator for serverISN, starts at 0
	checked int   // unexported: half-open entries examined by the most recent probe
}

// NewTable returns an empty table with nextISN == 0.
func NewTable() *Table { return &Table{half: map[string]Conn{}} }

// Lookup probes the table by src in O(1) and records how many half-open
// entries this probe examined (a single map key check).
func (t *Table) Lookup(src string) (Conn, bool) {
	t.checked = 1
	c, ok := t.half[src]
	return c, ok
}

// Open allocates the next monotonic serverISN and stores (or replaces) src's
// half-open connection {clientISN, serverISN}.
func (t *Table) Open(src string, clientISN int64) Conn {
	c := Conn{ClientISN: clientISN, ServerISN: t.next}
	t.next++
	t.half[src] = c
	return c
}

// Delete removes src from the half-open table (used when the handshake completes).
func (t *Table) Delete(src string) { delete(t.half, src) }

// VerifyProbeConstant is a self-contained verdict (never the counter value)
// that a single Lookup examines an h-independent constant number of
// half-open entries, at several table sizes from 100 to 10000.
func VerifyProbeConstant() error {
	for _, h := range []int{100, 1000, 10000} {
		tab := NewTable()
		for i := 0; i < h; i++ {
			tab.Open(fmt.Sprintf("s%05d", i), int64(i))
		}
		tab.Lookup("s00000") // hit: same primitive a retransmitted SYN uses
		if tab.checked > 1 {
			return fmt.Errorf("hs: hit probe not O(1) at h=%d", h)
		}
		tab.Lookup("missing") // miss: same primitive an orphan ACK uses
		if tab.checked > 1 {
			return fmt.Errorf("hs: miss probe not O(1) at h=%d", h)
		}
	}
	return nil
}
