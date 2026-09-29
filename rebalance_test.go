package rebalance

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"sync"
	"testing"
)

func newTestLogger(t *testing.T) Logger {
	return log.New(os.Stdout, fmt.Sprintf("[%s] ", t.Name()), log.Ltime|log.Lmicroseconds)
}

func hashMod(key string, n int) int {
	h := fnv.New64a()
	h.Write([]byte(key))
	return int(h.Sum64() % uint64(n))
}

func seedStore(t *testing.T, n int, keys ...string) *Store {
	t.Helper()
	s := New(n, newTestLogger(t))
	for _, k := range keys {
		if err := s.Put(k, "v-"+k); err != nil {
			t.Fatalf("seed Put(%q): %v", k, err)
		}
	}
	return s
}

// assertPlaced recomputes every key's home partition from its hash and
// requires the key to exist exactly there and nowhere else.
func assertPlaced(t *testing.T, s *Store, n int, keys []string) {
	t.Helper()
	parts := s.Partitions()
	if len(parts) < n {
		t.Fatalf("expected >= %d partitions, got %d", n, len(parts))
	}
	counts := make(map[string]int)
	for pid := 0; pid < n; pid++ {
		for k := range parts[pid] {
			counts[k]++
			if hashMod(k, n) != pid {
				t.Fatalf("key %q misplaced: P%d but hash%%%d=%d; %s", k, pid, n, hashMod(k, n), s.debugDump("verify-place"))
			}
		}
	}
	for _, k := range keys {
		if counts[k] != 1 {
			t.Fatalf("key %q appears %d times, want exactly 1; %s", k, counts[k], s.debugDump("verify-count"))
		}
	}
}

func TestDualRouteReadsAndWrites(t *testing.T) {
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	s := seedStore(t, 2, keys...)

	if err := s.BeginRebalance(3); err != nil {
		t.Fatalf("begin: %v", err)
	}
	phase, _, target, cursor, total := s.Progress()
	if phase != PhaseMigrating || target != 3 || cursor != 0 || total == 0 {
		t.Fatalf("unexpected progress: phase=%v target=%d %d/%d", phase, target, cursor, total)
	}

	if _, err := s.MigrateSteps(2); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, k := range keys {
		got, ok, err := s.Get(k)
		if err != nil || !ok || got != "v-"+k {
			t.Fatalf("Get(%q)=(%q,%t,%v)", k, got, ok, err)
		}
	}

	if err := s.Put("alpha", "updated"); err != nil {
		t.Fatalf("update existing key: %v", err)
	}
	if v, ok, _ := s.Get("alpha"); !ok || v != "updated" {
		t.Fatalf("updated value lost: %q %t", v, ok)
	}
	if err := s.Put("brand-new", "x"); !errors.Is(err, ErrNewKeyWhileMigrating) {
		t.Fatalf("new key err = %v, want ErrNewKeyWhileMigrating", err)
	}

	if _, err := s.MigrateSteps(0); err != nil {
		t.Fatalf("migrate rest: %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertPlaced(t, s, 3, keys)
	if v, ok, _ := s.Get("alpha"); !ok || v != "updated" {
		t.Fatalf("update did not survive commit: %q %t", v, ok)
	}
}

func TestExactlyOnceAndResume(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	s := seedStore(t, 3, keys...)

	if err := s.BeginRebalance(5); err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, _, _, _, total := s.Progress()

	if _, err := s.MigrateSteps(total / 2); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	snapAtCrash, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	_, _, _, crashedCursor, _ := s.Progress()

	restarted := New(1, newTestLogger(t))
	if err := restarted.Restore(snapAtCrash); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, _, cursor, total2 := restarted.Progress(); cursor != crashedCursor || total2 != total {
		t.Fatalf("cursor after resume = %d/%d, want %d/%d", cursor, total2, crashedCursor, total)
	}

	for _, k := range keys {
		v, ok, err := restarted.Get(k)
		if err != nil || !ok || v != "v-"+k {
			t.Fatalf("after resume Get(%q)=(%q,%t,%v)", k, v, ok, err)
		}
	}

	if _, err := restarted.MigrateSteps(0); err != nil {
		t.Fatalf("resume migrate: %v", err)
	}
	if _, _, _, cursor, total2 := restarted.Progress(); cursor != total2 {
		t.Fatalf("cursor = %d/%d", cursor, total2)
	}
	if err := restarted.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertPlaced(t, restarted, 5, keys)
}

func TestCommitBeforeCompleteRejected(t *testing.T) {
	s := seedStore(t, 2, "a", "b", "c", "d")

	if err := s.Commit(); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("commit when idle err = %v", err)
	}
	if _, err := s.MigrateSteps(1); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("migrate when idle err = %v", err)
	}

	if err := s.BeginRebalance(4); err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, beforeN, beforeTarget, beforeCursor, beforeTotal := s.Progress()

	if err := s.BeginRebalance(6); !errors.Is(err, ErrRebalanceInProgress) {
		t.Fatalf("duplicate begin err = %v", err)
	}
	if err := s.BeginRebalance(0); !errors.Is(err, ErrRebalanceInProgress) {
		t.Fatalf("duplicate begin while migrating err = %v", err)
	}

	if _, err := s.MigrateSteps(1); err != nil {
		t.Fatalf("migrate one: %v", err)
	}
	if err := s.Commit(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("early commit err = %v", err)
	}
	if err := s.Put("late-new-key", "v"); !errors.Is(err, ErrNewKeyWhileMigrating) {
		t.Fatalf("new key err = %v", err)
	}

	_, afterN, afterTarget, afterCursor, afterTotal := s.Progress()
	if afterN != beforeN || afterTarget != beforeTarget || afterCursor != beforeCursor+1 || afterTotal != beforeTotal {
		t.Fatalf("failure changed migration state: target=%d %d/%d vs %d/%d",
			afterTarget, beforeCursor+1, beforeTotal, afterCursor, afterTotal)
	}

	if _, err := s.MigrateSteps(0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got := map[string]string{}
	for _, p := range s.Partitions() {
		for k, v := range p {
			got[k] = v
		}
	}
	if len(got) != 4 {
		t.Fatalf("record set changed, got %d records", len(got))
	}
	for _, k := range []string{"a", "b", "c", "d"} {
		if got[k] != "v-"+k {
			t.Fatalf("key %q changed/lost: %q", k, got[k])
		}
	}
	assertPlaced(t, s, 4, []string{"a", "b", "c", "d"})
}

func TestInvalidTargetsAreAtomic(t *testing.T) {
	s := seedStore(t, 2, "a", "b")
	before, _ := s.Snapshot()

	for _, bad := range []int{0, -1, 2} {
		if err := s.BeginRebalance(bad); !errors.Is(err, ErrInvalidPartitionCount) {
			t.Fatalf("BeginRebalance(%d) err = %v", bad, err)
		}
	}
	after, _ := s.Snapshot()
	if string(after) != string(before) {
		t.Fatalf("invalid begin changed state")
	}
	if phase, n, target, cursor, total := s.Progress(); phase != PhaseIdle || n != 2 || target != 2 || cursor != 0 || total != 0 {
		t.Fatalf("state leaked after invalid begins: %v %d %d %d/%d", phase, n, target, cursor, total)
	}
}

func TestConcurrentReadsMatchSequential(t *testing.T) {
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	s := seedStore(t, 2, keys...)
	if err := s.BeginRebalance(4); err != nil {
		t.Fatalf("begin: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 17)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			done, err := s.MigrateSteps(1)
			if err != nil {
				errCh <- err
				return
			}
			if done == 0 {
				return
			}
		}
	}()

	for r := 0; r < 16; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for _, k := range keys {
					v, ok, err := s.Get(k)
					if err != nil {
						errCh <- err
						return
					}
					if !ok || v != "v-"+k {
						errCh <- fmt.Errorf("concurrent Get(%q)=(%q,%t)", k, v, ok)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	if err := s.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertPlaced(t, s, 4, keys)
}

func TestProgressMonotonic(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f"}
	s := seedStore(t, 2, keys...)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.BeginRebalance(3); err != nil {
			t.Errorf("begin: %v", err)
			return
		}
		for {
			done, err := s.MigrateSteps(1)
			if err != nil {
				t.Errorf("migrate: %v", err)
				return
			}
			if done == 0 {
				return
			}
		}
	}()

	lastCursor := -1
	lastTotal := 0
	for i := 0; i < 500; i++ {
		phase, _, _, cursor, total := s.Progress()
		if phase == PhaseMigrating {
			if cursor < lastCursor {
				t.Fatalf("cursor went backwards: %d -> %d", lastCursor, cursor)
			}
			if lastTotal != 0 && total != lastTotal {
				t.Fatalf("migration total changed: %d -> %d", lastTotal, total)
			}
			lastCursor, lastTotal = cursor, total
		}
	}
	wg.Wait()
	if err := s.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertPlaced(t, s, 3, keys)
}

func TestShrinkRebalance(t *testing.T) {
	keys := make([]string, 40)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%02d", i)
	}
	s := seedStore(t, 8, keys...)

	if err := s.BeginRebalance(3); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.MigrateSteps(10); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, k := range keys {
		if v, ok, _ := s.Get(k); !ok || v != "v-"+k {
			t.Fatalf("mid-shrink Get(%q) failed", k)
		}
	}
	if _, err := s.MigrateSteps(0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertPlaced(t, s, 3, keys)
}
