package migration

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type testLogger struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(fmt.Sprintf(format, args...))
	l.buf.WriteByte('\n')
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func appendVersion(version int) MigrationFunc {
	return func(value []byte) ([]byte, error) {
		result := make([]byte, 0, len(value)+4)
		result = append(result, value...)
		return append(result, fmt.Sprintf("->%d", version)...), nil
	}
}

func TestMultiVersionMigrationMatchesNaiveReference(t *testing.T) {
	logger := &testLogger{}
	store, err := NewStore[string](0, logger)
	if err != nil {
		t.Fatal(err)
	}

	original := []byte("v0")
	if err := store.Write("object", original); err != nil {
		t.Fatal(err)
	}
	original[0] = 'X'

	counts := make(map[int]*atomic.Int64)
	for version := 0; version < 3; version++ {
		counts[version] = &atomic.Int64{}
		target := version + 1
		version := version
		if err := store.RegisterMigration(version, func(value []byte) ([]byte, error) {
			counts[version].Add(1)
			return appendVersion(target)(value)
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Upgrade(1); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(2); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(3); err != nil {
		t.Fatal(err)
	}

	if version, err := store.StoredVersion("object"); err != nil || version != 0 {
		t.Fatalf("upgrades touched key: version=%d, err=%v", version, err)
	}

	got, err := store.Read("object")
	if err != nil {
		t.Fatal(err)
	}

	want := []byte("v0")
	for version := 1; version <= 3; version++ {
		want, err = appendVersion(version)(want)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("migrated value %q does not match naive reference %q", got, want)
	}

	if version, err := store.StoredVersion("object"); err != nil || version != 3 {
		t.Fatalf("write-back version=%d, err=%v", version, err)
	}

	again, err := store.Read("object")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, got) {
		t.Fatalf("repeat read changed result: %q != %q", again, got)
	}
	for version, count := range counts {
		if count.Load() != 1 {
			t.Fatalf("migration %d ran %d times, want once", version, count.Load())
		}
	}

	got[0] = 'Z'
	afterMutation, err := store.Read("object")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterMutation, want) {
		t.Fatalf("returned value aliases stored state: %q", afterMutation)
	}

	logs := logger.String()
	for _, wantLog := range []string{
		"migration chain decision: from=0 to=3 result=complete",
		"migration step input: key=object from=0 to=1 input=\"v0\"",
		"migration step return: key=object from=0 to=1 output=\"v0->1\"",
		"migration step decision: key=object from=0 result=success",
		"migration step input: key=object from=1 to=2 input=\"v0->1\"",
		"migration step return: key=object from=1 to=2 output=\"v0->1->2\"",
		"migration step decision: key=object from=1 result=success",
		"migration step input: key=object from=2 to=3 input=\"v0->1->2\"",
		"migration step return: key=object from=2 to=3 output=\"v0->1->2->3\"",
		"migration step decision: key=object from=2 result=success",
		"migration write-back decision: key=object from=0 to=3 result=committed",
	} {
		if !strings.Contains(logs, wantLog) {
			t.Fatalf("logs missing %q\nfull logs:\n%s", wantLog, logs)
		}
	}
}

func TestMissingChainPerformsNoMigrationCalls(t *testing.T) {
	store, err := NewStore[string](0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("object", []byte("v0")); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64
	if err := store.RegisterMigration(1, func(value []byte) ([]byte, error) {
		calls.Add(1)
		return appendVersion(1)(value)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(1); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(2); err != nil {
		t.Fatal(err)
	}

	_, err = store.Read("object")
	if !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("Read error = %v, want ErrMissingMigration", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("migration after missing predecessor ran %d times", calls.Load())
	}
	if version, versionErr := store.StoredVersion("object"); versionErr != nil || version != 0 {
		t.Fatalf("missing migration changed stored version to %d, err=%v", version, versionErr)
	}
	if value := storedValue(store, "object"); !bytes.Equal(value, []byte("v0")) {
		t.Fatalf("missing migration changed stored value to %q", value)
	}
	if store.CurrentVersion() != 2 {
		t.Fatalf("missing migration changed current version to %d", store.CurrentVersion())
	}
}

func TestMigrationFailureLeavesStorageUnchanged(t *testing.T) {
	store, err := NewStore[string](0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("object", []byte("v0")); err != nil {
		t.Fatal(err)
	}

	var firstCalls atomic.Int64
	var failingCalls atomic.Int64
	if err := store.RegisterMigration(0, func(value []byte) ([]byte, error) {
		firstCalls.Add(1)
		return appendVersion(1)(value)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterMigration(1, func(value []byte) ([]byte, error) {
		failingCalls.Add(1)
		return nil, fmt.Errorf("boom at %s", value)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(2); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		_, err = store.Read("object")
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("attempt %d error = %v, want ErrMigrationFailed", attempt+1, err)
		}
	}

	if version, versionErr := store.StoredVersion("object"); versionErr != nil || version != 0 {
		t.Fatalf("failed migration changed stored version to %d, err=%v", version, versionErr)
	}
	if value := storedValue(store, "object"); !bytes.Equal(value, []byte("v0")) {
		t.Fatalf("failed migration changed stored value to %q", value)
	}
	if firstCalls.Load() != 2 || failingCalls.Load() != 2 {
		t.Fatalf("retry calls = (%d,%d), want (2,2)", firstCalls.Load(), failingCalls.Load())
	}
}

func TestInvalidInputsAndRejectionsLeaveStateUnchanged(t *testing.T) {
	if _, err := NewStore[string](-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewStore(-1) error = %v", err)
	}

	store, err := NewStore[string](1, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Write("object", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("nil", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Write(nil) error = %v", err)
	}
	if err := store.RegisterMigration(-1, func(value []byte) ([]byte, error) {
		return value, nil
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RegisterMigration(-1) error = %v", err)
	}
	if err := store.RegisterMigration(0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RegisterMigration(nil) error = %v", err)
	}
	if err := store.RegisterMigration(0, func(value []byte) ([]byte, error) {
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterMigration(0, func(value []byte) ([]byte, error) {
		return value, nil
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate registration error = %v", err)
	}

	for _, version := range []int{-1, 0, 1} {
		if err := store.Upgrade(version); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Upgrade(%d) error = %v", version, err)
		}
	}
	for _, version := range []int{-1, 2} {
		if err := store.CheckIntegrity(version); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("CheckIntegrity(%d) error = %v", version, err)
		}
	}

	if _, err := store.Read("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read(missing) error = %v", err)
	}
	if _, err := store.StoredVersion("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("StoredVersion(missing) error = %v", err)
	}

	if store.CurrentVersion() != 1 {
		t.Fatalf("current version changed to %d", store.CurrentVersion())
	}
	if version, err := store.StoredVersion("object"); err != nil || version != 1 {
		t.Fatalf("stored version changed to %d, err=%v", version, err)
	}
	value, err := store.Read("object")
	if err != nil || !bytes.Equal(value, []byte("v1")) {
		t.Fatalf("stored value changed to %q, err=%v", value, err)
	}
	if err := store.CheckIntegrity(0); err != nil {
		t.Fatalf("valid registration was not retained: %v", err)
	}
}

func TestConcurrentReadsShareOneMigration(t *testing.T) {
	store, err := NewStore[string](0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("object", []byte("v0")); err != nil {
		t.Fatal(err)
	}

	enteredFirst := make(chan struct{})
	releaseFirst := make(chan struct{})
	enteredSecond := make(chan struct{})
	releaseSecond := make(chan struct{})
	var firstCalls atomic.Int64
	var secondCalls atomic.Int64

	if err := store.RegisterMigration(0, func(value []byte) ([]byte, error) {
		firstCalls.Add(1)
		close(enteredFirst)
		<-releaseFirst
		return appendVersion(1)(value)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterMigration(1, func(value []byte) ([]byte, error) {
		secondCalls.Add(1)
		close(enteredSecond)
		<-releaseSecond
		return appendVersion(2)(value)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upgrade(2); err != nil {
		t.Fatal(err)
	}

	leaderResult := make(chan readResult, 1)
	go func() {
		value, err := store.Read("object")
		leaderResult <- readResult{value: value, err: err}
	}()
	<-enteredFirst

	const readers = 32
	results := make([]readResult, readers)
	var readerWG sync.WaitGroup
	for i := 0; i < readers; i++ {
		readerWG.Add(1)
		go func(index int) {
			defer readerWG.Done()
			value, err := store.Read("object")
			results[index] = readResult{value: value, err: err}
		}(i)
	}

	close(releaseFirst)
	<-enteredSecond
	close(releaseSecond)

	leader := <-leaderResult
	readerWG.Wait()

	want := []byte("v0->1->2")
	if leader.err != nil || !bytes.Equal(leader.value, want) {
		t.Fatalf("leader result = %q, %v", leader.value, leader.err)
	}
	for index, result := range results {
		if result.err != nil || !bytes.Equal(result.value, want) {
			t.Fatalf("reader %d result = %q, %v", index, result.value, result.err)
		}
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("migration calls = (%d,%d), want (1,1)", firstCalls.Load(), secondCalls.Load())
	}

	again, err := store.Read("object")
	if err != nil || !bytes.Equal(again, want) {
		t.Fatalf("post-write-back read = %q, %v", again, err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("post-write-back migration calls = (%d,%d), want (1,1)", firstCalls.Load(), secondCalls.Load())
	}
}

func TestConcurrentOperations(t *testing.T) {
	store, err := NewStore[string](3, nil)
	if err != nil {
		t.Fatal(err)
	}
	for version := 0; version < 3; version++ {
		if err := store.RegisterMigration(version, appendVersion(version+1)); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 16
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			key := fmt.Sprintf("shared-%d", worker%4)
			for iteration := 0; iteration < 50; iteration++ {
				if err := store.Write(key, []byte(fmt.Sprintf("v3-%d-%d", worker, iteration))); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
				if _, err := store.Read(key); err != nil {
					t.Errorf("Read: %v", err)
					return
				}
				if _, err := store.StoredVersion(key); err != nil {
					t.Errorf("StoredVersion: %v", err)
					return
				}
				if store.CurrentVersion() != 3 {
					t.Errorf("CurrentVersion = %d", store.CurrentVersion())
					return
				}
				if err := store.CheckIntegrity(0); err != nil {
					t.Errorf("CheckIntegrity: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

type readResult struct {
	value []byte
	err   error
}

func storedValue(store *Store[string], key string) []byte {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]byte(nil), store.data[key].value...)
}
