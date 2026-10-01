package halfmessage

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type testStatus int

const (
	testPending testStatus = iota
	testCommitted
	testRolledBack
)

type naiveMessage struct {
	id        string
	payload   string
	status    testStatus
	position  uint64
	attempts  uint64
	createdAt int64
	nextCheck int64
	reason    RollbackReason
}

type naiveLog struct {
	cfg       Config
	now       int64
	order     []string
	messages  map[string]*naiveMessage
	committed []CommittedMessage[string]
}

type testEvent struct {
	op  string
	id  string
	now int64
}

type callbackResult struct {
	before  CheckDecision
	answer  CheckDecision
	sendID  string
	payload string
}

func newNaive(cfg Config) *naiveLog {
	return &naiveLog{
		cfg:      cfg,
		messages: make(map[string]*naiveMessage),
	}
}

func (n *naiveLog) send(id, payload string, now int64) error {
	if now < n.now {
		return ErrClockMovedBack
	}
	n.now = now
	if _, ok := n.messages[id]; ok {
		return ErrDuplicateTransaction
	}
	msg := &naiveMessage{
		id:        id,
		payload:   payload,
		status:    testPending,
		createdAt: now,
		nextCheck: now + n.cfg.FirstCheckAfter,
	}
	n.order = append(n.order, id)
	n.messages[id] = msg
	return nil
}

func (n *naiveLog) commit(id string) error {
	msg, ok := n.messages[id]
	if !ok {
		return ErrUnknownTransaction
	}
	if msg.status == testCommitted {
		return ErrAlreadyCommitted
	}
	if msg.status == testRolledBack {
		return ErrAlreadyRolledBack
	}
	msg.status = testCommitted
	msg.position = uint64(len(n.committed))
	n.committed = append(n.committed, CommittedMessage[string]{
		Position: msg.position,
		TxID:     msg.id,
		Payload:  msg.payload,
	})
	return nil
}

func (n *naiveLog) rollback(id string) error {
	msg, ok := n.messages[id]
	if !ok {
		return ErrUnknownTransaction
	}
	if msg.status == testCommitted {
		return ErrAlreadyCommitted
	}
	if msg.status == testRolledBack {
		return ErrAlreadyRolledBack
	}
	msg.status = testRolledBack
	msg.reason = RollbackExplicit
	return nil
}

func (n *naiveLog) tick(now int64, decide func(string, uint64, int64) callbackResult) error {
	if now < n.now {
		return ErrClockMovedBack
	}
	n.now = now
	due := make([]*naiveMessage, 0)
	for _, id := range n.order {
		msg := n.messages[id]
		if msg.status == testPending && msg.nextCheck <= now {
			due = append(due, msg)
		}
	}
	for _, msg := range due {
		if msg.status != testPending {
			continue
		}
		msg.attempts++
		result := decide(msg.id, msg.attempts, now)
		if result.before == CheckCommit {
			_ = n.commit(msg.id)
		}
		if result.before == CheckRollback {
			_ = n.rollback(msg.id)
		}
		if result.sendID != "" {
			_ = n.send(result.sendID, result.payload, now)
		}
		if msg.status != testPending {
			continue
		}
		switch {
		case msg.attempts >= n.cfg.MaxChecks && result.answer == CheckUnknown:
			msg.status = testRolledBack
			msg.reason = RollbackChecksExhausted
		case result.answer == CheckCommit:
			_ = n.commit(msg.id)
		case result.answer == CheckRollback:
			_ = n.rollback(msg.id)
		default:
			msg.nextCheck = now + n.cfg.CheckInterval
		}
	}
	return nil
}

func TestReplayMatchesNaiveSimulation(t *testing.T) {
	cfg := Config{FirstCheckAfter: 10, CheckInterval: 7, MaxChecks: 3}
	script := map[string][]callbackResult{
		"A": {
			{answer: CheckUnknown, sendID: "X", payload: "x"},
			{answer: CheckCommit},
		},
		"C": {
			{answer: CheckUnknown},
			{answer: CheckUnknown},
			{answer: CheckUnknown},
		},
		"D": {{before: CheckCommit, answer: CheckUnknown}},
		"E": {
			{answer: CheckUnknown},
			{answer: CheckRollback},
		},
		"X": {
			{answer: CheckUnknown},
			{answer: CheckUnknown},
			{answer: CheckUnknown},
		},
	}

	naive := newNaive(cfg)
	var actual *Log[string]
	callback := func(id string, payload string, attempt uint64, now int64) CheckDecision {
		result := script[id][attempt-1]
		t.Logf("callback input: id=%q payload=%q attempt=%d now=%d; rule: before-callback=%d returned=%d", id, payload, attempt, now, result.before, result.answer)
		if result.sendID != "" {
			if err := actual.Send(result.sendID, result.payload, now); err != nil {
				t.Fatalf("callback send %q: %v", result.sendID, err)
			}
			t.Logf("callback output: sent %q during tick; it is excluded from this tick's fixed due-set", result.sendID)
		}
		if result.before == CheckCommit {
			if err := actual.Commit(id); err != nil {
				t.Fatalf("callback commit %q: %v", id, err)
			}
			t.Logf("callback output: committed %q before returning; return value %d must be ignored", id, result.answer)
		}
		if result.before == CheckRollback {
			if err := actual.Rollback(id); err != nil {
				t.Fatalf("callback rollback %q: %v", id, err)
			}
		}
		return result.answer
	}
	actual, err := New[string](cfg, callback)
	if err != nil {
		t.Fatal(err)
	}

	events := []testEvent{
		{op: "send", id: "A", now: 1},
		{op: "send", id: "B", now: 2},
		{op: "send", id: "C", now: 3},
		{op: "send", id: "D", now: 4},
		{op: "send", id: "E", now: 5},
		{op: "tick", now: 9},
		{op: "tick", now: 10},
		{op: "tick", now: 11},
		{op: "commit", id: "B"},
		{op: "tick", now: 17},
		{op: "send", id: "A", now: 18},
		{op: "commit", id: "Z"},
		{op: "tick", now: 24},
		{op: "commit", id: "D"},
		{op: "rollback", id: "D"},
		{op: "commit", id: "E"},
		{op: "rollback", id: "E"},
		{op: "tick", now: 31},
		{op: "tick", now: 38},
	}

	for _, event := range events {
		var actualErr, modelErr error
		switch event.op {
		case "send":
			t.Logf("input: send id=%q now=%d; pending and invisible until commit", event.id, event.now)
			actualErr = actual.Send(event.id, strings.ToLower(event.id), event.now)
			modelErr = naive.send(event.id, strings.ToLower(event.id), event.now)
		case "commit":
			t.Logf("input: commit id=%q; position is allocated at commit time", event.id)
			actualErr = actual.Commit(event.id)
			modelErr = naive.commit(event.id)
		case "rollback":
			t.Logf("input: rollback id=%q", event.id)
			actualErr = actual.Rollback(event.id)
			modelErr = naive.rollback(event.id)
		case "tick":
			t.Logf("input: tick now=%d; due-set and creation order are fixed before callbacks", event.now)
			actualErr = actual.Tick(event.now)
			modelErr = naive.tick(event.now, func(id string, attempt uint64, now int64) callbackResult {
				result := script[id][attempt-1]
				if result.sendID != "" {
					_ = naive.send(result.sendID, result.payload, now)
				}
				return result
			})
		}
		t.Logf("output: event=%s id=%q actual=%v model=%v", event.op, event.id, actualErr, modelErr)
		if !errors.Is(actualErr, modelErr) {
			t.Fatalf("error mismatch for %+v: actual=%v model=%v", event, actualErr, modelErr)
		}
		assertStatesEqual(t, actual, naive)
	}

	assertStatesEqual(t, actual, naive)
	committed := actual.CommittedMessages()
	t.Logf("final committed log: %#v", committed)
	if positions := []uint64{committed[0].Position, committed[1].Position, committed[2].Position}; fmt.Sprint(positions) != "[0 1 2]" {
		t.Fatalf("positions not continuous: %v", positions)
	}
	if order := []string{committed[0].TxID, committed[1].TxID, committed[2].TxID}; fmt.Sprint(order) != "[B D A]" {
		t.Fatalf("positions do not follow commit order: %v", order)
	}
	infos := infoByID(actual.Messages())
	if infos["A"].Attempts != 2 {
		t.Fatalf("A first callback at F and next interval from actual callback: got %d attempts", infos["A"].Attempts)
	}
	if infos["C"].Attempts != 3 || infos["C"].Status != RolledBack || infos["C"].RollbackReason != RollbackChecksExhausted {
		t.Fatalf("third unknown did not exhaust checks: %+v", infos["C"])
	}
	if infos["D"].Attempts != 1 || infos["D"].Status != Committed {
		t.Fatalf("callback return after in-callback commit was not ignored: %+v", infos["D"])
	}
}

func TestConfigurationAndClock(t *testing.T) {
	callback := func(string, string, uint64, int64) CheckDecision { return CheckUnknown }
	for _, cfg := range []Config{
		{FirstCheckAfter: -1, CheckInterval: 1, MaxChecks: 1},
		{FirstCheckAfter: 0, CheckInterval: 0, MaxChecks: 1},
		{FirstCheckAfter: 0, CheckInterval: 1, MaxChecks: 0},
	} {
		if _, err := New[string](cfg, callback); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("cfg %+v: got %v", cfg, err)
		}
	}
	log, err := New[string](Config{FirstCheckAfter: 0, CheckInterval: 1, MaxChecks: 1}, callback)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Send("A", "a", 2); err != nil {
		t.Fatal(err)
	}
	if err := log.Tick(1); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("backward tick: got %v", err)
	}
	if err := log.Send("B", "b", 1); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("backward send: got %v", err)
	}
}

func TestFirstCheckBoundaryAndMissedIntervals(t *testing.T) {
	cfg := Config{FirstCheckAfter: 10, CheckInterval: 5, MaxChecks: 3}
	model := newNaive(cfg)
	var calls []uint64
	log, err := New[string](cfg, func(_ string, _ string, attempt uint64, now int64) CheckDecision {
		t.Logf("callback input: attempt=%d now=%d;判定依据=到点集合在 Tick 开始时固定", attempt, now)
		calls = append(calls, attempt)
		return CheckUnknown
	})
	if err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		op  string
		now int64
	}{
		{op: "send", now: 0},
		{op: "tick", now: 9},
		{op: "tick", now: 10},
		{op: "tick", now: 30},
	}
	for _, step := range steps {
		var actualErr, modelErr error
		if step.op == "send" {
			t.Logf("input: send at now=%d; first due time is creation+F=10", step.now)
			actualErr = log.Send("A", "a", step.now)
			modelErr = model.send("A", "a", step.now)
		} else {
			t.Logf("input: tick now=%d; next interval starts from previous actual callback time", step.now)
			actualErr = log.Tick(step.now)
			modelErr = model.tick(step.now, func(string, uint64, int64) callbackResult {
				return callbackResult{answer: CheckUnknown}
			})
		}
		t.Logf("output: actual=%v model=%v calls=%v", actualErr, modelErr, calls)
		if actualErr != modelErr {
			t.Fatalf("error mismatch: actual=%v model=%v", actualErr, modelErr)
		}
		assertStatesEqual(t, log, model)
	}

	if fmt.Sprint(calls) != "[1 2]" {
		t.Fatalf("expected exactly one first callback and one delayed callback, got %v", calls)
	}
	info := infoByID(log.Messages())["A"]
	if info.Attempts != 2 || info.NextCheckAt != 35 {
		t.Fatalf("interval should restart at actual tick 30: %+v", info)
	}
}

func TestCallbackRunsWithoutInternalLock(t *testing.T) {
	release := make(chan struct{})
	committedOutside := make(chan struct{})
	cfg := Config{FirstCheckAfter: 0, CheckInterval: 1, MaxChecks: 1}
	log, err := New[string](cfg, func(id string, _ string, _ uint64, _ int64) CheckDecision {
		if id != "A" {
			return CheckCommit
		}
		close(committedOutside)
		select {
		case <-release:
		case <-time.After(time.Second):
			t.Error("callback held internal lock; external commit could not proceed")
			return CheckRollback
		}
		return CheckUnknown
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Send("A", "a", 0); err != nil || log.Send("B", "b", 0) != nil {
		t.Fatal(err)
	}
	go func() {
		<-committedOutside
		if err := log.Commit("B"); err != nil {
			t.Errorf("commit during callback: %v", err)
		}
		close(release)
	}()
	if err := log.Tick(0); err != nil {
		t.Fatal(err)
	}
	infos := infoByID(log.Messages())
	if infos["B"].Status != Committed || infos["B"].Position != 0 {
		t.Fatalf("external commit during callback failed: %+v", infos["B"])
	}
	if infos["A"].Status != RolledBack || infos["A"].RollbackReason != RollbackChecksExhausted {
		t.Fatalf("A should exhaust its only check: %+v", infos["A"])
	}
}

func TestConcurrentSendsTicksAndCommits(t *testing.T) {
	const count = 100
	cfg := Config{FirstCheckAfter: 10, CheckInterval: 10, MaxChecks: 2}
	log, err := New[string](cfg, func(string, string, uint64, int64) CheckDecision {
		return CheckCommit
	})
	if err != nil {
		t.Fatal(err)
	}
	var sends sync.WaitGroup
	for i := range count {
		sends.Add(1)
		go func(i int) {
			defer sends.Done()
			id := fmt.Sprintf("tx-%03d", i)
			if err := log.Send(id, id, 0); err != nil {
				t.Errorf("send %s: %v", id, err)
			}
		}(i)
	}
	sends.Wait()

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := log.Tick(10); err != nil {
			t.Errorf("tick: %v", err)
		}
	}()
	for i := range count {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			id := fmt.Sprintf("tx-%03d", i)
			err := log.Commit(id)
			if err != nil && !errors.Is(err, ErrAlreadyCommitted) {
				t.Errorf("commit %s: %v", id, err)
			}
		}(i)
	}
	workers.Wait()

	committed := log.CommittedMessages()
	if len(committed) != count {
		t.Fatalf("committed count = %d, want %d", len(committed), count)
	}
	seen := make(map[uint64]string, count)
	for _, msg := range committed {
		if previous, ok := seen[msg.Position]; ok {
			t.Fatalf("position %d used by %q and %q", msg.Position, previous, msg.TxID)
		}
		seen[msg.Position] = msg.TxID
	}
	for position := range uint64(count) {
		if _, ok := seen[position]; !ok {
			t.Fatalf("missing log position %d", position)
		}
	}
	for _, info := range log.Messages() {
		if info.Status != Committed {
			t.Fatalf("%s reached non-commit terminal %d", info.TxID, info.Status)
		}
	}
}

func infoByID(infos []MessageInfo[string]) map[string]MessageInfo[string] {
	result := make(map[string]MessageInfo[string], len(infos))
	for _, info := range infos {
		result[info.TxID] = info
	}
	return result
}

func assertStatesEqual(t *testing.T, actual *Log[string], model *naiveLog) {
	t.Helper()
	actualInfos := infoByID(actual.Messages())
	if len(actualInfos) != len(model.messages) {
		t.Fatalf("message count actual=%d model=%d", len(actualInfos), len(model.messages))
	}
	for id, expected := range model.messages {
		got := actualInfos[id]
		if got.Payload != expected.payload ||
			statusToTest(got.Status) != expected.status ||
			got.Position != expected.position ||
			got.Attempts != expected.attempts ||
			got.CreatedAt != expected.createdAt ||
			got.NextCheckAt != expected.nextCheck ||
			got.RollbackReason != expected.reason {
			t.Fatalf("state mismatch for %q: actual=%+v model=%+v", id, got, expected)
		}
	}
	actualCommitted := actual.CommittedMessages()
	if len(actualCommitted) != len(model.committed) {
		t.Fatalf("committed count actual=%d model=%d", len(actualCommitted), len(model.committed))
	}
	for i, expected := range model.committed {
		if actualCommitted[i] != expected {
			t.Fatalf("committed log at %d: actual=%+v model=%+v", i, actualCommitted[i], expected)
		}
	}
}

func statusToTest(status Status) testStatus {
	switch status {
	case Committed:
		return testCommitted
	case RolledBack:
		return testRolledBack
	default:
		return testPending
	}
}
