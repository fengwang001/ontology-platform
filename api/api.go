// Package api is the public facade: one in-process store with master R0 and
// followers R1/R2, and sessions with read-your-writes consistency.
package api

import (
	"errors"
	"fmt"

	"ontology/kv"
	"ontology/ses"
)

// Decidable sentinel errors; the three fault classes are mutually distinct.
var (
	ErrEmptyKey  = ses.ErrEmptyKey
	ErrSyncIndex = ses.ErrSyncIndex
	ErrClosed    = ses.ErrSessionClosed
	ErrInvariant = errors.New("api: self-check invariant violated")
)

type Store struct{ c *ses.Cluster }
type Session struct{ s *ses.Session }

// New creates an empty store; Open creates a session pinned to follower R1.
func New() *Store                           { return &Store{c: ses.NewCluster()} }
func (st *Store) Open() *Session            { return &Session{s: ses.NewSession()} }
func (st *Store) Close(s *Session)          { s.s.Close() }
func (st *Store) Sync(idx int) error        { return st.c.Sync(idx) }
func (st *Store) View() map[string]kv.Entry { return st.c.View() }
func (st *Store) Snap() (r0, r1, r2 map[string]kv.Entry) {
	return st.c.Snapshots()
}

// Write applies one write to master R0 and records the session write version.
func (st *Store) Write(s *Session, key, val string) (int64, error) {
	return st.c.Write(s.s, key, val)
}

// Read returns a value this session can see (ver >= its write version),
// falling back to R0 when sticky follower R1 is stale.
func (st *Store) Read(s *Session, key string) (string, int64, error) {
	return st.c.Read(s.s, key)
}

// WriteVersion returns the session's last written version for key (session
// state, not the routing-check counter).
func (s *Session) WriteVersion(key string) int64 { return s.s.WriteVersion(key) }

// SelfCheck runs a built-in operation sequence and verifies the four
// invariants: read-your-writes, naive-reference equality, monotonic versions
// across Sync, and no-trace rejection.
func (st *Store) SelfCheck() error {
	c, s := ses.NewCluster(), ses.NewSession()
	ref := map[string]kv.Entry{}
	write := func(key, val string) {
		v, err := c.Write(s, key, val)
		if err != nil {
			panic(err)
		}
		ref[key] = kv.Entry{Val: val, Ver: v}
	}
	readEq := func(key string) bool {
		val, ver, err := c.Read(s, key)
		return err == nil && ver == ref[key].Ver && val == ref[key].Val
	}

	write("k", "A") // mandated seven-step sequence on k
	if !readEq("k") {
		return fmt.Errorf("%w: read-your-writes step 2", ErrInvariant)
	}
	if err := c.Sync(1); err != nil {
		return err
	}
	write("k", "B")
	if err := c.Sync(2); err != nil {
		return err
	}
	write("k", "C")
	if !readEq("k") {
		return fmt.Errorf("%w: read-your-writes step 7", ErrInvariant)
	}

	for i := 0; i < 50; i++ { // reference equality across keys/rewrites
		key := fmt.Sprintf("k%02d", i)
		for j := 0; j <= i%3; j++ {
			write(key, fmt.Sprintf("v%d", j))
			if !readEq(key) {
				return fmt.Errorf("%w: reference mismatch on %s", ErrInvariant, key)
			}
		}
	}
	for _, idx := range []int{1, 2} { // versions never regress across Sync
		_, r1, r2 := c.Snapshots()
		old := []map[string]kv.Entry{nil, r1, r2}[idx]
		if err := c.Sync(idx); err != nil {
			return err
		}
		_, n1, n2 := c.Snapshots()
		now := []map[string]kv.Entry{nil, n1, n2}[idx]
		for key, o := range old {
			if now[key].Ver < o.Ver {
				return fmt.Errorf("%w: Sync regressed %s", ErrInvariant, key)
			}
		}
	}

	view := c.View() // rejected ops leave no trace
	if _, err := c.Write(s, "", "x"); !errors.Is(err, ses.ErrEmptyKey) {
		return fmt.Errorf("%w: empty key", ErrInvariant)
	}
	if err := c.Sync(9); !errors.Is(err, ses.ErrSyncIndex) {
		return fmt.Errorf("%w: sync index", ErrInvariant)
	}
	closed := ses.NewSession()
	closed.Close()
	if _, err := c.Write(closed, "q", "1"); !errors.Is(err, ses.ErrSessionClosed) {
		return fmt.Errorf("%w: closed write", ErrInvariant)
	}
	if _, _, err := c.Read(closed, "q"); !errors.Is(err, ses.ErrSessionClosed) {
		return fmt.Errorf("%w: closed read", ErrInvariant)
	}
	if !mapsEq(c.View(), view) {
		return fmt.Errorf("%w: rejection changed state", ErrInvariant)
	}
	return nil
}

func mapsEq(a, b map[string]kv.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
