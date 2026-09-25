// Package api is the outward face of the read-your-writes store.
// Depends on ses (which depends on kv); never the reverse.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/kv"
	"ontology/ses"
)

// Distinguishable sentinel errors, re-exported from ses.
var (
	ErrEmptyKey = ses.ErrEmptyKey
	ErrBadIndex = ses.ErrBadIndex
	ErrClosed   = ses.ErrClosed
)

// Store is one three-replica cluster (R0 primary, R1/R2 followers).
type Store struct{ c *ses.Cluster }

// New returns an empty store.
func New() *Store { return &Store{c: ses.NewCluster()} }

// Open creates a session with independent writeVer state.
func (s *Store) Open() *Session { return &Session{in: s.c.Open()} }

// Sync makes follower idx (1 or 2) catch up with the primary.
func (s *Store) Sync(idx int) error { return s.c.Sync(idx) }

// View snapshots all three replicas: [0]=R0, [1]=R1, [2]=R2.
func (s *Store) View() [3]map[string]kv.Entry { return s.c.Snapshot() }

// Session is one client's handle; not shared across goroutines.
type Session struct{ in *ses.Session }

// Write stores val under key on the primary and returns its version.
func (s *Session) Write(key, val string) (int64, error) { return s.in.Write(key, val) }

// Read returns (val, ver) honouring read-your-writes.
func (s *Session) Read(key string) (string, int64, error) {
	e, err := s.in.Read(key)
	return e.Val, e.Ver, err
}

// Close shuts the session; later reads/writes fail with ErrClosed.
func (s *Session) Close() { s.in.Close() }

// SelfCheck runs a built-in operation sequence against a fresh store and
// verifies the four invariants. A nil return means all of them hold.
func (s *Store) SelfCheck() error {
	st := New()
	a, b := st.Open(), st.Open()
	ref := map[string]kv.Entry{}    // naive reference: all writes in order
	hist := map[string][]kv.Entry{} // key -> entries indexed by version
	wv := map[*Session]map[string]int64{a: {}, b: {}}
	prev := st.View()
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("k%d", i%6)
		sess := a
		if i%2 == 1 {
			sess = b
		}
		v, err := sess.Write(key, fmt.Sprintf("v%d", i))
		if err != nil {
			return err
		}
		wv[sess][key] = v
		e := kv.Entry{Val: fmt.Sprintf("v%d", i), Ver: v}
		ref[key] = e
		for int64(len(hist[key])) <= v {
			hist[key] = append(hist[key], kv.Entry{})
		}
		hist[key][v] = e
		if i%3 == 0 {
			if err := st.Sync(1 + i%2); err != nil {
				return err
			}
		}
		val, ver, err := sess.Read(key)
		if err != nil {
			return err
		}
		if ver < wv[sess][key] { // invariant 1: read-your-writes
			return fmt.Errorf("inv1: read ver %d < writeVer %d", ver, wv[sess][key])
		}
		if hist[key][ver].Val != val { // invariant 2: matches naive reference at ver
			return fmt.Errorf("inv2: (%s,%d) not in reference history", val, ver)
		}
		cur := st.View()
		for r := 0; r < 3; r++ { // invariant 3: versions never regress
			for k, e := range cur[r] {
				if prev[r][k].Ver > e.Ver {
					return fmt.Errorf("inv3: replica %d key %s regressed", r, k)
				}
			}
		}
		prev = cur
	}
	if !reflect.DeepEqual(prev[0], ref) { // invariant 2: primary == reference
		return errors.New("inv2: primary diverged from naive reference")
	}
	before := st.View() // invariant 4: rejected ops leave no trace
	a.Close()
	rej := []error{ses.ErrClosed, ses.ErrClosed, ses.ErrEmptyKey, ses.ErrEmptyKey, ses.ErrBadIndex, ses.ErrBadIndex}
	_, e1 := a.Write("x", "y")
	_, _, e2 := a.Read("x")
	_, e3 := b.Write("", "y")
	_, _, e4 := b.Read("")
	got := []error{e1, e2, e3, e4, st.Sync(0), st.Sync(3)}
	for i := range got {
		if !errors.Is(got[i], rej[i]) {
			return fmt.Errorf("inv4: rejection %d = %v, want %v", i, got[i], rej[i])
		}
	}
	if !reflect.DeepEqual(before, st.View()) {
		return errors.New("inv4: rejected operation changed state")
	}
	if _, err := b.Write("k0", "z"); err != nil { // still usable afterwards
		return fmt.Errorf("inv4: store unusable after rejections: %w", err)
	}
	return nil
}
