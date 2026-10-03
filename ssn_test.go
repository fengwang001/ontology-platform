package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustRead(t *testing.T, a *Authenticator, txID int, key int) int {
	t.Helper()
	value, err := a.Read(txID, key)
	if err != nil {
		t.Fatalf("Read(tx=%d, key=%d): %v", txID, key, err)
	}
	return value
}

func mustWrite(t *testing.T, a *Authenticator, txID int, key int, value int) {
	t.Helper()
	if err := a.Write(txID, key, value); err != nil {
		t.Fatalf("Write(tx=%d, key=%d, value=%d): %v", txID, key, value, err)
	}
}

func assertCommit(t *testing.T, a *Authenticator, txID int, wantCommit int) {
	t.Helper()
	commitID, reason, err := a.Commit(txID)
	t.Logf("Commit(tx=%d) => c=%d, reason=%d, err=%v", txID, commitID, reason, err)
	if err != nil || reason != AbortNone || commitID != wantCommit {
		t.Fatalf("Commit(tx=%d) = (%d, %d, %v), want (%d, %d, nil)", txID, commitID, reason, err, wantCommit, AbortNone)
	}
}

func assertAbortReason(t *testing.T, a *Authenticator, txID int, want AbortReason, wantErr error) {
	t.Helper()
	commitID, reason, err := a.Commit(txID)
	t.Logf("Commit(tx=%d) => c=%d, reason=%d, err=%v; want reason=%d", txID, commitID, reason, err, want)
	if commitID != 0 || reason != want || !errors.Is(err, wantErr) {
		t.Fatalf("Commit(tx=%d) = (%d, %d, %v), want abort (0, %d, %v)", txID, commitID, reason, err, want, wantErr)
	}
}

func TestWriteSkew(t *testing.T) {
	a, err := NewAuthenticator(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := a.Begin(), a.Begin()
	mustRead(t, a, t1, 0)
	mustRead(t, a, t1, 1)
	mustRead(t, a, t2, 0)
	mustRead(t, a, t2, 1)
	mustWrite(t, a, t1, 0, 10)
	mustWrite(t, a, t2, 1, 20)

	t.Logf("T1 input reads={0,1}, writes={0}; basis eta=0, pi=min(1,+inf)=1")
	assertCommit(t, a, t1, 1)

	t.Logf("T2 input reads={0,1}, writes={1}; basis eta=max(0,key1.ps=1)=1, pi=min(2,key0.ss=1)=1")
	assertAbortReason(t, a, t2, AbortSerializationSafetyNet, ErrSerializationSafetyNet)
	if a.state.commitNumber != 1 {
		t.Fatalf("n after SSN abort = %d, want 1", a.state.commitNumber)
	}
}

func TestExclusionWindowBoundary(t *testing.T) {
	a, err := NewAuthenticator(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	setup := a.Begin()
	mustWrite(t, a, setup, 0, 1)
	assertCommit(t, a, setup, 1)

	tx := a.Begin()
	mustRead(t, a, tx, 0)
	mustWrite(t, a, tx, 0, 2)
	t.Logf("difference-one passes: eta=1, pi=min(2,+inf)=2")
	assertCommit(t, a, tx, 2)

	t.Logf("equality is covered by TestWriteSkew: eta=pi=1 is rejected")
}

func TestReadOnlyAdvancesCommitAndRaisesPS(t *testing.T) {
	a, err := NewAuthenticator(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	writer := a.Begin()
	mustWrite(t, a, writer, 0, 7)
	assertCommit(t, a, writer, 1)

	reader := a.Begin()
	if value := mustRead(t, a, reader, 0); value != 7 {
		t.Fatalf("read-only value = %d, want 7", value)
	}
	t.Logf("read-only commit consumes c=2 and raises selected version ps to 2")
	assertCommit(t, a, reader, 2)
}

func TestReadOnlyAnomaly(t *testing.T) {
	a, err := NewAuthenticator(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := a.Begin(), a.Begin()
	mustRead(t, a, t1, 1)
	mustWrite(t, a, t1, 1, 1)
	mustRead(t, a, t2, 0)
	mustRead(t, a, t2, 1)
	mustWrite(t, a, t2, 0, 2)
	assertCommit(t, a, t1, 1)

	t3 := a.Begin()
	mustRead(t, a, t3, 0)
	mustRead(t, a, t3, 1)
	t.Logf("T3 read-only: eta=1, pi=2, commit=2")
	assertCommit(t, a, t3, 2)

	t.Logf("T2 after T3: eta=max(0,key0.ps=2)=2, pi=min(3,key1.ss=1)=1")
	assertAbortReason(t, a, t2, AbortSerializationSafetyNet, ErrSerializationSafetyNet)
}

func TestWriteSetPSParticipatesInEta(t *testing.T) {
	a, err := NewAuthenticator(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := a.Begin(), a.Begin()
	mustRead(t, a, t1, 1)
	mustRead(t, a, t2, 0)
	mustRead(t, a, t2, 1)
	mustWrite(t, a, t1, 1, 1)
	mustWrite(t, a, t2, 0, 2)
	assertCommit(t, a, t1, 1)

	reader := a.Begin()
	mustRead(t, a, reader, 0)
	assertCommit(t, a, reader, 2)

	t.Logf("eta must include write-set key0.ps=2; without that term T2 would pass")
	assertAbortReason(t, a, t2, AbortSerializationSafetyNet, ErrSerializationSafetyNet)
}

func TestSuccessorWaterUsesPi(t *testing.T) {
	a, err := NewAuthenticator(3, 3)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2, t3 := a.Begin(), a.Begin(), a.Begin()
	mustRead(t, a, t1, 2)
	mustWrite(t, a, t1, 0, 1)
	assertCommit(t, a, t1, 1)

	mustRead(t, a, t2, 0)
	mustWrite(t, a, t2, 1, 2)
	t.Logf("T2 sets old key1 ss=pi=1, not commit c=2")
	assertCommit(t, a, t2, 2)

	mustRead(t, a, t3, 1)
	mustWrite(t, a, t3, 2, 3)
	t.Logf("T3: eta=key2.ps=1, pi=key1.ss=1; using c=2 would incorrectly pass")
	assertAbortReason(t, a, t3, AbortSerializationSafetyNet, ErrSerializationSafetyNet)
}

func TestWriteWriteConflictBeforeSSN(t *testing.T) {
	a, err := NewAuthenticator(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := a.Begin(), a.Begin()
	mustRead(t, a, t1, 0)
	mustRead(t, a, t1, 1)
	mustRead(t, a, t2, 0)
	mustRead(t, a, t2, 1)
	mustWrite(t, a, t1, 0, 1)
	mustWrite(t, a, t2, 0, 2)
	assertCommit(t, a, t1, 1)

	t.Logf("T2 has both WW and SSN danger; write-write conflict is checked and reported first")
	assertAbortReason(t, a, t2, AbortWriteWriteConflict, ErrWriteWriteConflict)
}

func TestEvictedVersionWatermarks(t *testing.T) {
	a, err := NewAuthenticator(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	t1 := a.Begin()
	mustRead(t, a, t1, 0)
	mustWrite(t, a, t1, 1, 1)

	t2 := a.Begin()
	mustWrite(t, a, t2, 0, 2)
	assertCommit(t, a, t2, 1)

	t3 := a.Begin()
	mustWrite(t, a, t3, 0, 3)
	assertCommit(t, a, t3, 2)

	t4 := a.Begin()
	mustRead(t, a, t4, 1)
	assertCommit(t, a, t4, 3)

	if len(a.state.versions[0]) != 2 || a.state.versions[0][0].cs != 1 {
		t.Fatalf("key0 was not truncated to cs={1,2}: %+v", a.state.versions[0])
	}
	t.Logf("T1 still holds evicted key0 version cs=0 with ss=1; eta=3, pi=1")
	assertAbortReason(t, a, t1, AbortSerializationSafetyNet, ErrSerializationSafetyNet)
}

func TestSnapshotTooOldDoesNotChangeState(t *testing.T) {
	a, err := NewAuthenticator(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	oldReader := a.Begin()
	firstWriter := a.Begin()
	mustWrite(t, a, firstWriter, 0, 1)
	assertCommit(t, a, firstWriter, 1)

	secondWriter := a.Begin()
	mustWrite(t, a, secondWriter, 0, 2)
	assertCommit(t, a, secondWriter, 2)

	before := a.state.versions[0][0]
	_, err = a.Read(oldReader, 0)
	if !errors.Is(err, ErrSnapshotTooOld) {
		t.Fatalf("Read = %v, want ErrSnapshotTooOld", err)
	}
	if a.state.commitNumber != 2 || len(a.state.versions[0]) != 2 || a.state.versions[0][0] != before {
		t.Fatalf("snapshot-too-old changed state")
	}
	if err := a.Abort(oldReader); err != nil {
		t.Fatalf("rejected read finished the transaction: %v", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	a, err := NewAuthenticator(64, 8)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 64; worker++ {
		wg.Add(1)
		go func(key int) {
			defer wg.Done()
			txID := a.Begin()
			_, _ = a.Read(txID, key)
			_ = a.Write(txID, key, key+1)
			_, _, _ = a.Commit(txID)
		}(worker)
	}
	wg.Wait()
	if a.state.commitNumber != 64 {
		t.Fatalf("n = %d, want 64", a.state.commitNumber)
	}
}

func TestInvalidConfigAndCallOrdering(t *testing.T) {
	for _, params := range [][2]int{{0, 2}, {65, 2}, {1, 1}, {1, 9}} {
		if _, err := NewAuthenticator(params[0], params[1]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewAuthenticator(%d, %d) = %v", params[0], params[1], err)
		}
	}
	a, _ := NewAuthenticator(1, 2)
	if _, err := a.Read(404, 0); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("missing tx Read error = %v", err)
	}
	if err := a.Write(404, 0, 1); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("missing tx Write error = %v", err)
	}
	if _, _, err := a.Commit(404); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("missing tx Commit error = %v", err)
	}
	if err := a.Abort(404); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("missing tx Abort error = %v", err)
	}

	txID := a.Begin()
	if err := a.Abort(txID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Read(txID, 0); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("finished tx Read error = %v", err)
	}
	if err := a.Write(txID, 0, 1); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("finished tx Write error = %v", err)
	}
	if _, _, err := a.Commit(txID); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("finished tx Commit error = %v", err)
	}
	if err := a.Abort(txID); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("finished tx Abort error = %v", err)
	}
	if _, err := a.Read(a.Begin(), 1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("key range error = %v", err)
	}
}
