package pbftlog_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/pbftlog"
)

// send logs the input, applies one message and logs the output plus the
// rejection reason, so the certificate evidence is visible with `go test -v`.
func send(t *testing.T, l *pbftlog.Log, kind pbftlog.Kind, m pbftlog.Message) error {
	t.Helper()
	err := l.Handle(kind, m)
	if err != nil {
		t.Logf("input  %s(v=%d,s=%d,d=%q,from=%d) -> REJECT: %v",
			kind, m.View, m.Seq, m.Digest, m.From, err)
	} else {
		t.Logf("input  %s(v=%d,s=%d,d=%q,from=%d) -> accept",
			kind, m.View, m.Seq, m.Digest, m.From)
	}
	return err
}

func mustSend(t *testing.T, l *pbftlog.Log, kind pbftlog.Kind, m pbftlog.Message) {
	t.Helper()
	if err := send(t, l, kind, m); err != nil {
		t.Fatalf("unexpected rejection for %s %+v: %v", kind, m, err)
	}
}

func assertReject(t *testing.T, l *pbftlog.Log, kind pbftlog.Kind, m pbftlog.Message, want error) {
	t.Helper()
	err := send(t, l, kind, m)
	if !errors.Is(err, want) {
		t.Fatalf("%s %+v: want %v, got %v", kind, m, want, err)
	}
}

func runExecute(t *testing.T, l *pbftlog.Log, want []pbftlog.ExecutedEntry) {
	t.Helper()
	got := l.Execute()
	t.Logf("input  Execute -> executed=%d, output=%v", l.Executed(), got)
	if len(got) != len(want) {
		t.Fatalf("Execute len: want %d (%v), got %v", len(want), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Execute[%d]: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

func backups(n int, primary int) []int {
	out := make([]int, 0, n-1)
	for i := 0; i < n; i++ {
		if i != primary {
			out = append(out, i)
		}
	}
	return out
}

func certInfo(l *pbftlog.Log, seq int, digest string) string {
	return fmt.Sprintf("Prepared=%v CommittedLocal=%v",
		l.Prepared(seq, digest), l.CommittedLocal(seq, digest))
}

func TestNewValidation(t *testing.T) {
	if _, err := pbftlog.New(0, 0, 4); !errors.Is(err, pbftlog.ErrInvalidFaultBound) {
		t.Fatalf("f=0: %v", err)
	}
	if _, err := pbftlog.New(1, 0, 0); !errors.Is(err, pbftlog.ErrInvalidLimit) {
		t.Fatalf("L=0: %v", err)
	}
	if _, err := pbftlog.New(1, -1, 4); !errors.Is(err, pbftlog.ErrInvalidView) {
		t.Fatalf("view=-1: %v", err)
	}

	l, err := pbftlog.New(2, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if l.N() != 7 || l.View() != 3 || l.Primary() != 3 || l.Limit() != 5 || l.Executed() != 0 {
		t.Fatalf("unexpected params: N=%d v=%d p=%d L=%d executed=%d",
			l.N(), l.View(), l.Primary(), l.Limit(), l.Executed())
	}
	t.Logf("f=2,view=3 -> N=%d primary=%d L=%d", l.N(), l.Primary(), l.Limit())
}

func TestPreparedThreshold(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			l, _ := pbftlog.New(f, 0, 8)
			p := l.Primary()
			bps := backups(l.N(), p)
			d := "d1"

			mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: p})
			if l.Prepared(1, d) {
				t.Fatal("prepared before any prepare")
			}
			for _, from := range bps[:2*f-1] {
				mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: from})
			}
			t.Logf("after %d prepares: %s", 2*f-1, certInfo(l, 1, d))
			if l.Prepared(1, d) {
				t.Fatalf("prepared with only %d prepares", 2*f-1)
			}
			mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: bps[2*f-1]})
			t.Logf("after %d prepares: %s", 2*f, certInfo(l, 1, d))
			if !l.Prepared(1, d) {
				t.Fatalf("not prepared with exactly %d prepares", 2*f)
			}
			if l.CommittedLocal(1, d) {
				t.Fatal("committed-local without any commit")
			}
		})
	}
}

func TestPrepareFromPrimaryRejected(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0})
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0},
		pbftlog.ErrPrepareFromPrimary)
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 1})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 2})
	if !l.Prepared(1, "d") {
		t.Fatal("expected prepared from the two real backups")
	}
}

func TestDigestMismatchPrepareNotCounted(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "good", From: 0})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "bad", From: 1})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "good", From: 2})
	t.Logf("one mismatching prepare: %s", certInfo(l, 1, "good"))
	if l.Prepared(1, "good") {
		t.Fatal("prepare with a different digest must not count")
	}
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "good", From: 1},
		pbftlog.ErrConflictingPrepare)
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "good", From: 3})
	if !l.Prepared(1, "good") {
		t.Fatal("expected prepared once a second distinct backup agrees")
	}
}

func TestDuplicatePrepareCountedOnce(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 1})
	for i := 0; i < 3; i++ {
		mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 1})
	}
	if l.Prepared(1, "d") {
		t.Fatal("duplicate prepare from one replica counted more than once")
	}
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 2})
	if !l.Prepared(1, "d") {
		t.Fatal("expected prepared with two distinct backups")
	}
}

func makePrepared(t *testing.T, l *pbftlog.Log, seq int, digest string) {
	t.Helper()
	f := (l.N() - 1) / 3
	p := l.Primary()
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: l.View(), Seq: seq, Digest: digest, From: p})
	for _, from := range backups(l.N(), p)[:2*f] {
		mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: l.View(), Seq: seq, Digest: digest, From: from})
	}
}

func makeCommitted(t *testing.T, l *pbftlog.Log, seq int, digest string) {
	t.Helper()
	f := (l.N() - 1) / 3
	makePrepared(t, l, seq, digest)
	senders := append([]int{l.Primary()}, backups(l.N(), l.Primary())[:2*f]...)
	for _, from := range senders {
		mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: l.View(), Seq: seq, Digest: digest, From: from})
	}
}

func TestCommittedLocalThreshold(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			l, _ := pbftlog.New(f, 0, 8)
			d := "d1"
			makePrepared(t, l, 1, d)
			senders := append([]int{l.Primary()}, backups(l.N(), l.Primary())[:2*f]...)
			for _, from := range senders[:2*f] {
				mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: from})
			}
			t.Logf("after %d commits: %s", 2*f, certInfo(l, 1, d))
			if l.CommittedLocal(1, d) {
				t.Fatalf("committed-local with only %d commits", 2*f)
			}
			mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: senders[2*f]})
			t.Logf("after %d commits: %s", 2*f+1, certInfo(l, 1, d))
			if !l.CommittedLocal(1, d) {
				t.Fatalf("not committed-local with exactly %d commits", 2*f+1)
			}
		})
	}
}

func TestCommitArrivesBeforePrepare(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	d := "d1"
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 0})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 1})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 2})
	if l.CommittedLocal(1, d) {
		t.Fatal("committed-local before pre-prepare/prepare")
	}
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 0})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 1})
	t.Logf("commits predate prepare, one prepare present: %s", certInfo(l, 1, d))
	if l.CommittedLocal(1, d) {
		t.Fatal("committed-local with only one prepare")
	}
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 2})
	if !l.CommittedLocal(1, d) {
		t.Fatal("expected committed-local once prepared with commits already present")
	}
}

func TestDuplicateAndMismatchCommit(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	d := "d"
	makePrepared(t, l, 1, d)
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 0})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 0})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: "x", From: 1})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 2})
	if l.CommittedLocal(1, d) {
		t.Fatal("mismatching/duplicate commits inflated the count")
	}
	assertReject(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 1},
		pbftlog.ErrConflictingCommit)
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 3})
	if !l.CommittedLocal(1, d) {
		t.Fatal("expected committed-local with three distinct matching commits")
	}
}

func TestPrePrepareRules(t *testing.T) {
	l, _ := pbftlog.New(1, 1, 8)
	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 1, Seq: 1, Digest: "d", From: 0},
		pbftlog.ErrPrePrepareFromBackup)
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 1, Seq: 1, Digest: "d", From: 1})
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 1, Seq: 1, Digest: "d", From: 1})
	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 1, Seq: 1, Digest: "other", From: 1},
		pbftlog.ErrConflictingPrePrepare)
}

func TestWindowBoundariesAndAdvance(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 2)
	d := "d"
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 0})
	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 3, Digest: d, From: 0},
		pbftlog.ErrSeqOutOfWindow)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 0, Digest: d, From: 1},
		pbftlog.ErrSeqOutOfWindow)

	makeCommitted(t, l, 1, d)
	runExecute(t, l, []pbftlog.ExecutedEntry{{Seq: 1, Digest: d}})

	// Window slid to (1,3]: seq 3 is now accepted, late seq 1 rejected.
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 3, Digest: d, From: 0})
	assertReject(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: d, From: 3},
		pbftlog.ErrSeqOutOfWindow)

	// Finish seq 2 whose pre-prepare was recorded before execution advanced.
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 1})
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 2})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 0})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 1})
	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 2})
	runExecute(t, l, []pbftlog.ExecutedEntry{{Seq: 2, Digest: d}})

	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 2, Digest: d, From: 3},
		pbftlog.ErrSeqOutOfWindow)
}

func TestStrictlyAscendingExecution(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	d1, d2 := "d1", "d2"

	// Seq 2 becomes committed-local while seq 1 has nothing.
	makeCommitted(t, l, 2, d2)
	runExecute(t, l, nil)
	if l.Executed() != 0 {
		t.Fatalf("seq 2 must not execute while seq 1 is open, executed=%d", l.Executed())
	}

	// Complete seq 1: one Execute call runs 1 then 2 in order.
	makeCommitted(t, l, 1, d1)
	runExecute(t, l, []pbftlog.ExecutedEntry{
		{Seq: 1, Digest: d1},
		{Seq: 2, Digest: d2},
	})
	if l.Executed() != 2 {
		t.Fatalf("executed=%d, want 2", l.Executed())
	}
	runExecute(t, l, nil)
}

func TestExecuteResultNotAliased(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	d := "d"
	makeCommitted(t, l, 1, d)
	first := l.Execute()
	t.Logf("input Execute -> output=%v", first)
	if len(first) != 1 {
		t.Fatalf("want 1 executed entry, got %v", first)
	}
	first[0].Digest = "tampered"
	if got := l.Execute(); len(got) != 0 {
		t.Fatalf("second Execute must be empty, got %v", got)
	}
}

func TestRejectionDoesNotMutateState(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)
	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0})

	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 9},
		pbftlog.ErrSenderOutOfRange)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "", From: 1},
		pbftlog.ErrEmptyDigest)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 7, Seq: 1, Digest: "d", From: 1},
		pbftlog.ErrWrongView)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0},
		pbftlog.ErrPrepareFromPrimary)
	assertReject(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 9, Digest: "d", From: 1},
		pbftlog.ErrSeqOutOfWindow)

	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 1})
	if l.Prepared(1, "d") {
		t.Fatal("rejected messages changed prepare state")
	}
	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 2})
	if !l.Prepared(1, "d") {
		t.Fatal("expected prepared after the two legitimate prepares")
	}

	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "evil", From: 0},
		pbftlog.ErrConflictingPrePrepare)
	if !l.Prepared(1, "d") || l.Prepared(1, "evil") {
		t.Fatal("conflicting pre-prepare mutated stored digest")
	}
}

func TestErrorPriority(t *testing.T) {
	l, _ := pbftlog.New(1, 0, 8)

	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 9, Seq: 99, Digest: "", From: 40},
		pbftlog.ErrSenderOutOfRange)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 9, Seq: 99, Digest: "", From: 0},
		pbftlog.ErrEmptyDigest)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 9, Seq: 99, Digest: "d", From: 0},
		pbftlog.ErrWrongView)

	mustSend(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0})
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 9, Digest: "d", From: 0},
		pbftlog.ErrSeqOutOfWindow)
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 0},
		pbftlog.ErrPrepareFromPrimary)
	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "x", From: 1},
		pbftlog.ErrPrePrepareFromBackup)

	mustSend(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 1})
	assertReject(t, l, pbftlog.Prepare, pbftlog.Message{View: 0, Seq: 1, Digest: "x", From: 1},
		pbftlog.ErrConflictingPrepare)
	assertReject(t, l, pbftlog.PrePrepare, pbftlog.Message{View: 0, Seq: 1, Digest: "x", From: 0},
		pbftlog.ErrConflictingPrePrepare)

	mustSend(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: "d", From: 2})
	assertReject(t, l, pbftlog.Commit, pbftlog.Message{View: 0, Seq: 1, Digest: "x", From: 2},
		pbftlog.ErrConflictingCommit)
}
