// Command demo exercises the two-phase commit coordinator end to end.
// It takes no arguments, uses no network, and exits with code 0.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/coord"
	"ontology/participant"
	"ontology/vote"
)

var passed, failed int

func check(name string, cond bool) {
	if cond {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTxn(ids map[string]vote.Ballot, order ...string) (*coord.Txn, map[string]*participant.Participant, *clock) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	txn := coord.NewTxn(c.now, time.Minute)
	members := map[string]*participant.Participant{}
	for _, id := range order {
		p := participant.New(id, ids[id])
		members[id] = p
		_ = txn.Register(p)
	}
	return txn, members, c
}

func main() {
	// 1. Unanimous agree commits.
	txn, m, _ := newTxn(map[string]vote.Ballot{"a": vote.BallotAgree, "b": vote.BallotAgree}, "a", "b")
	d := txn.Drive()
	check("unanimous agree commits", d.Verdict == vote.VerdictCommit &&
		m["a"].State() == participant.StateCommitted && m["b"].State() == participant.StateCommitted)

	// 2. One reject aborts everyone, including those who agreed.
	txn, m, _ = newTxn(map[string]vote.Ballot{"a": vote.BallotAgree, "b": vote.BallotReject}, "a", "b")
	d = txn.Drive()
	check("one reject aborts all", d.Verdict == vote.VerdictAbort && d.Reason == vote.ReasonRejected &&
		d.Culprit == "b" && m["a"].State() == participant.StateAborted)

	// 3. Timeout aborts with a reason distinct from reject.
	txn, m, c := newTxn(map[string]vote.Ballot{"slow": vote.BallotAgree}, "slow")
	m["slow"].SetPrepareHook(func() { c.advance(time.Minute) }) // answers exactly at the deadline
	d = txn.Drive()
	check("timeout aborts, reason distinct", d.Verdict == vote.VerdictAbort &&
		d.Reason == vote.ReasonTimeout && d.Culprit == "slow")

	// 4. A recorded decision cannot be rewritten.
	first := txn.Drive()
	c.advance(time.Hour)
	check("decision is write-once", txn.Drive() == first)

	// 5. Repeated instructions are idempotent.
	txn, m, _ = newTxn(map[string]vote.Ballot{"a": vote.BallotAgree}, "a")
	txn.Drive()
	_ = txn.Send("a", coord.InstrCommit)
	_ = txn.Send("a", coord.InstrCommit)
	check("repeated commit idempotent", m["a"].CommitCount() == 1)

	// 6. Illegal transitions are distinguishable errors.
	pending := participant.New("p", vote.BallotAgree)
	e1 := pending.Commit()
	aborted := participant.New("q", vote.BallotReject)
	aborted.Prepare()
	e2 := aborted.Commit()
	committed := participant.New("r", vote.BallotAgree)
	committed.Prepare()
	committed.Commit()
	e3 := committed.Abort()
	check("illegal transitions distinct", errors.Is(e1, participant.ErrNotPrepared) &&
		errors.Is(e2, participant.ErrCommitAfterAbort) && errors.Is(e3, participant.ErrAbortAfterCommit) &&
		pending.State() == participant.StatePending)

	// 7. Empty participant set commits (vacuous truth).
	txn, _, _ = newTxn(nil)
	d = txn.Drive()
	check("empty set commits", d.Verdict == vote.VerdictCommit && d.Reason == vote.ReasonAllAgreed)

	// 8. Undecided query reports the zero decision, not a guess.
	txn, _, _ = newTxn(map[string]vote.Ballot{"a": vote.BallotAgree}, "a")
	snap := txn.Query()
	check("undecided query is zero value", snap.Decision == vote.Decision{} && snap.Phase == coord.PhaseVoting)

	// 9. Recovery replay keeps states and commit counts.
	txn, m, _ = newTxn(map[string]vote.Ballot{"a": vote.BallotAgree, "b": vote.BallotAgree}, "a", "b")
	txn.Drive()
	before := txn.Query()
	_ = txn.Replay()
	after := txn.Query()
	check("replay after crash consistent", before.Decision == after.Decision &&
		m["a"].CommitCount() == 1 && m["b"].CommitCount() == 1 &&
		m["a"].State() == participant.StateCommitted)

	// 10. Concurrent drives decide exactly once.
	txn, m, _ = newTxn(map[string]vote.Ballot{"a": vote.BallotAgree, "b": vote.BallotAgree}, "a", "b")
	const workers = 32
	results := make([]vote.Decision, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) { defer wg.Done(); results[w] = txn.Drive() }(w)
	}
	wg.Wait()
	unique := true
	for w := 1; w < workers; w++ {
		unique = unique && results[w] == results[0]
	}
	check("concurrent decision unique", unique && m["a"].CommitCount() == 1 && m["b"].CommitCount() == 1)

	fmt.Printf("TOTAL %d/%d passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
