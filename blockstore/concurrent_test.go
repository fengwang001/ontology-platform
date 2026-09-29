package blockstore

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Heavy concurrent interleaving of upload/commit/read/Mark/Sweep: every block
// of every committed snapshot must remain readable at every observation.
func TestConcurrentInterleavingSafety(t *testing.T) {
	st := New(Config{Capacity: 1 << 20})

	const writers = 8
	const rounds = 50

	// Shared pool of contents so writers frequently reuse each other's
	// blocks, including blocks a Mark may have just made pending.
	pool := make([][]byte, 12)
	for i := range pool {
		pool[i] = []byte(fmt.Sprintf("block-%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Continuous reader: anything still referenced by a committed snapshot
	// must read back while it observes the snapshot.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, snap := range st.SnapshotIDs() {
				st.mu.Lock()
				refs := append([]string(nil), st.manifest[snap]...)
				st.mu.Unlock()
				for _, digest := range refs {
					if _, err := st.Read(digest); err != nil {
						t.Errorf("committed snapshot %q lost block %s: %v",
							snap, digest[:8], err)
						return
					}
				}
			}
		}
	}()

	// Concurrent GC driver.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			st.Mark()
			if _, err := st.Sweep(); err != nil {
				// ErrNoGCCycle only happens if a Mark got interleaved oddly;
				// Mark always installs a cycle, so this should not occur.
				t.Errorf("unexpected Sweep error: %v", err)
				return
			}
		}
	}()

	committed := make(chan string, writers*rounds)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				id := fmt.Sprintf("w%d-r%d", w, r)
				if err := st.BeginSession(id); err != nil {
					t.Errorf("BeginSession %s: %v", id, err)
					return
				}
				n := 1 + (r % 3)
				refs := make([]string, 0, n)
				for k := 0; k < n; k++ {
					data := pool[(w+r+k)%len(pool)]
					digest, err := st.Upload(id, data)
					if err != nil {
						t.Errorf("Upload %s: %v", id, err)
						return
					}
					refs = append(refs, digest)
				}
				snap := fmt.Sprintf("snap-%d-%d", w, r)
				if err := st.Commit(id, snap, refs); err != nil {
					t.Errorf("Commit %s: %v", snap, err)
					return
				}
				committed <- snap
			}
		}(w)
	}

	// Verify snapshots as they commit, then drain and stop.
	expected := writers * rounds
	seen := make(map[string]bool, expected)
	for len(seen) < expected {
		snap := <-committed
		seen[snap] = true
		refs := st.manifestLookup(snap)
		for _, digest := range refs {
			if _, err := st.Read(digest); err != nil {
				t.Fatalf("freshly committed %s lost block %s: %v",
					snap, digest[:8], err)
			}
		}
	}
	close(stop)
	wg.Wait()

	// Post-quiescent invariant: a full GC cycle deletes no reachable block.
	st.Mark()
	if _, err := st.Sweep(); err != nil {
		t.Fatalf("final Sweep: %v", err)
	}
	for snap := range seen {
		for _, digest := range st.manifestLookup(snap) {
			if _, err := st.Read(digest); err != nil {
				t.Fatalf("final check: snapshot %s lost block %s: %v",
					snap, digest[:8], err)
			}
		}
	}
}

func (s *Store) manifestLookup(snap string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.manifest[snap]...)
}

// Determinism: the same operation sequence and the same interleaving order
// always yields the same deleted set.
func TestDeterministicDeletionSet(t *testing.T) {
	run := func() []string {
		st := New(Config{})
		mustBegin(t, st, "a")
		d1 := mustUpload(t, st, "a", []byte("alpha"))
		d2 := mustUpload(t, st, "a", []byte("beta"))
		d3 := mustUpload(t, st, "a", []byte("gamma"))
		d4 := mustUpload(t, st, "a", []byte("delta"))
		mustCommit(t, st, "a", "s1", []string{d1, d2})

		st.Mark()
		mustBegin(t, st, "b")
		dReuseBeta := mustUpload(t, st, "b", []byte("beta")) // reuse pending d2
		_ = dReuseBeta
		dReuse := mustUpload(t, st, "b", []byte("gamma")) // reuse pending d3
		mustCommit(t, st, "b", "s2", []string{d3, dReuse})
		st.Sweep()

		// Collect which digests are physically gone.
		var gone []string
		for _, d := range []string{d1, d2, d3, d4} {
			if StateOfIsDeleted(st, d) {
				gone = append(gone, d)
			}
		}
		return gone
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !equalStrings(got, first) {
			t.Fatalf("run %d deleted %v, want %v", i, got, first)
		}
	}
}

func StateOfIsDeleted(st *Store, digest string) bool {
	return st.StateOf(digest) == Deleted
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The log records inputs, outputs and the reason behind each decision.
func TestLoggingRecordsInputsOutputsAndReasons(t *testing.T) {
	var buf bytes.Buffer
	st := New(Config{Capacity: 9, Log: &buf})

	if err := st.BeginSession("s"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upload("s", []byte("too-large")); err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if _, err := st.Upload("s", []byte("x")); err == nil {
		t.Fatal("expected capacity error")
	}
	st.Mark()
	if _, err := st.Sweep(); err != nil {
		t.Fatal(err)
	}

	log := buf.String()
	for _, want := range []string{
		"msg=BeginSession", "session=s",
		"msg=Upload", "result=rejected",
		"reason=\"blockstore: storage capacity full\"",
		"msg=Mark", "decision=",
		"msg=Sweep", "decision=",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
