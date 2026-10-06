package gradeaudit

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{ReviewWindow: 5, MinScore: 0, MaxScore: 100, MaxScoreDelta: 10, ApprovalLimit: 3,
		RequiredApproverLevel: 2, SpecialApproverLevel: 4, SpecialConfirmTTL: 2, RequiredLockLevel: 5,
		Teachers: map[CourseID]ActorID{"math": "teacher", "english": "teacher"}}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := NewEngine(testConfig(), map[ActorID]int{"teacher": 1, "boss": 2, "locker": 5, "sp1": 4, "sp2": 4})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func key1() RecordKey { return RecordKey{Student: "alice", Course: "math", Semester: "s1"} }

func seed(t *testing.T, e *Engine, key RecordKey, at int64, score int) {
	t.Helper()
	if err := e.EnterInitialScore(at, "teacher", key, score); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != code {
		t.Fatalf("err=%v want=%s", err, code)
	}
}

func snap(t *testing.T, e *Engine, key RecordKey, at, now int64, score int, source Source, review bool) {
	t.Helper()
	got, err := e.SnapshotAt(key, at, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasScore || got.Score != score || got.Source != source || got.UnderReview != review {
		t.Fatalf("snapshot=%+v want score=%d source=%s review=%v", got, score, source, review)
	}
}

func setupReviewed(e *Engine, key RecordKey, initialAt, applyAt, proposeAt int64, score int) {
	_ = e.ApplyReview(applyAt, ActorID(key.Student), key)
	_ = e.ProposeChange(proposeAt, "teacher", key, score)
}

func TestInclusiveBoundaries(t *testing.T) {
	e := testEngine(t)
	key := key1()
	seed(t, e, key, 10, 80)
	if err := e.ApplyReview(15, "alice", key); err != nil {
		t.Fatalf("window endpoint: %v", err)
	}
	seed(t, e, RecordKey{Student: "bob", Course: "math", Semester: "s1"}, 16, 80)
	key2 := RecordKey{Student: "bob", Course: "math", Semester: "s1"}
	setupReviewed(e, key2, 0, 17, 18, 90)
	if err := e.DecideProposal(21, "boss", key2, true, "delta/deadline endpoint"); err != nil {
		t.Fatalf("decision endpoint: %v", err)
	}
	snap(t, e, key2, 21, 21, 90, SourceReview, false)
	snap(t, e, key, 15, 21, 80, SourceInitial, true)
}

func TestLazyExpirationAndRepropose(t *testing.T) {
	for name, touch := range map[string]func(*Engine, RecordKey) error{
		"proposal": func(e *Engine, k RecordKey) error { return e.ProposeChange(6, "teacher", k, 82) },
		"reject":   func(e *Engine, k RecordKey) error { return e.RejectApplication(6, "teacher", k, "x") },
		"apply":    func(e *Engine, k RecordKey) error { return e.ApplyReview(6, "alice", k) },
	} {
		t.Run(name, func(t *testing.T) {
			e := testEngine(t)
			key := key1()
			seed(t, e, key, 0, 80)
			setupReviewed(e, key, 0, 1, 2, 82)
			wantCode(t, touch(e, key), ErrTimeout)
			if e.AuditLog()[len(e.AuditLog())-1].Kind != AuditProposalExpired {
				t.Fatal("missing lazy expiration audit")
			}
			if err := e.ProposeChange(7, "teacher", key, 79); err != nil {
				t.Fatalf("repropose: %v", err)
			}
			if err := e.DecideProposal(8, "boss", key, false, "no"); err != nil {
				t.Fatal(err)
			}
			if err := e.ProposeChange(9, "teacher", key, 75); err != nil {
				t.Fatalf("after reject propose: %v", err)
			}
		})
	}
}

func TestLockAndSpecialChannel(t *testing.T) {
	e := testEngine(t)
	key := key1()
	seed(t, e, key, 0, 80)
	if err := e.ApplyReview(1, "alice", key); err != nil {
		t.Fatal(err)
	}
	key2 := RecordKey{Student: "bob", Course: "math", Semester: "s1"}
	seed(t, e, key2, 1, 70)
	setupReviewed(e, key2, 0, 2, 2, 75)
	if err := e.LockSemester(3, "locker", "s1"); err != nil {
		t.Fatal(err)
	}
	snap(t, e, key, 4, 4, 80, SourceInitial, false)
	wantCode(t, e.ProposeChange(4, "teacher", key, 99), ErrLocked)

	done, err := e.SpecialConfirm(5, "sp1", key, 99)
	if err != nil || done {
		t.Fatalf("first confirm: %v done=%v", err, done)
	}
	_, err = e.SpecialConfirm(8, "sp2", key, 99)
	wantCode(t, err, ErrTimeout)
	done, err = e.SpecialConfirm(9, "sp1", key, 99)
	if err != nil || done {
		t.Fatalf("fresh first: %v done=%v", err, done)
	}
	done, err = e.SpecialConfirm(11, "sp2", key, 99)
	if err != nil || !done {
		t.Fatalf("second: %v done=%v", err, done)
	}
	snap(t, e, key, 11, 11, 99, SourceSpecial, false)
}

func TestSnapshotAndAverage(t *testing.T) {
	e := testEngine(t)
	key := key1()
	seed(t, e, key, 0, 60)
	other := RecordKey{Student: "alice", Course: "english", Semester: "s1"}
	seed(t, e, other, 0, 90)
	setupReviewed(e, key, 0, 1, 2, 70)
	if err := e.DecideProposal(3, "boss", key, true, "v2"); err != nil {
		t.Fatal(err)
	}
	if err := e.LockSemester(4, "locker", "s1"); err != nil {
		t.Fatal(err)
	}
	_, _ = e.SpecialConfirm(5, "sp1", key, 95)
	_, _ = e.SpecialConfirm(7, "sp2", key, 95)
	snap(t, e, key, 0, 7, 60, SourceInitial, false)
	snap(t, e, key, 2, 7, 60, SourceInitial, true)
	snap(t, e, key, 3, 7, 70, SourceReview, false)
	snap(t, e, key, 7, 7, 95, SourceSpecial, false)

	avg, ok, err := e.SemesterAverageAt("alice", "s1", 0, 7)
	if err != nil || !ok || avg != 75 {
		t.Fatalf("avg=%v ok=%v err=%v", avg, ok, err)
	}
}

func TestConcurrencyAndReplay(t *testing.T) {
	run := func() int {
		e := testEngine(t)
		key := key1()
		seed(t, e, key, 0, 80)
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = e.ApplyReview(int64(i+1), "alice", key) }(i)
		}
		wg.Wait()
		return len(e.AuditLog())
	}
	if first, second := run(), run(); first != second {
		t.Fatalf("accepted operation count %d != %d", first, second)
	}
}

func TestDeterministicSerialReplay(t *testing.T) {
	run := func() []AuditEntry {
		e := testEngine(t)
		key := key1()
		seed(t, e, key, 0, 80)
		_ = e.ApplyReview(1, "alice", key)
		_ = e.ProposeChange(2, "teacher", key, 84)
		_ = e.DecideProposal(3, "boss", key, true, "ok")
		return e.AuditLog()
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("entry %d differs: %+v != %+v", i, first[i], second[i])
		}
	}
}

func TestErrorPriority(t *testing.T) {
	cases := []struct {
		name string
		code ErrorCode
		run  func(*Engine, RecordKey) error
	}{
		{"invalid", ErrInvalid, func(e *Engine, k RecordKey) error { return e.ApplyReview(-1, "", k) }},
		{"clock", ErrClock, func(e *Engine, k RecordKey) error {
			return e.ApplyReview(0, ActorID(k.Student), RecordKey{Student: "missing", Course: "math", Semester: "s1"})
		}},
		{"missing", ErrNotFound, func(e *Engine, k RecordKey) error {
			_ = e.LockSemester(11, "locker", "s1")
			return e.ApplyReview(12, "ghost", RecordKey{Student: "ghost", Course: "math", Semester: "s1"})
		}},
		{"locked", ErrLocked, func(e *Engine, k RecordKey) error {
			_ = e.LockSemester(11, "locker", "s1")
			return e.ApplyReview(12, "intruder", k)
		}},
		{"permission", ErrPermission, func(e *Engine, k RecordKey) error {
			return e.ProposeChange(12, "boss", k, 82)
		}},
		{"timeout", ErrTimeout, func(e *Engine, k RecordKey) error {
			return e.ApplyReview(16, "alice", k)
		}},
		{"state", ErrState, func(e *Engine, k RecordKey) error {
			_ = e.ApplyReview(12, "alice", k)
			return e.ApplyReview(13, "alice", k)
		}},
		{"score", ErrScore, func(e *Engine, k RecordKey) error {
			_ = e.ApplyReview(12, "alice", k)
			return e.ProposeChange(13, "teacher", k, 999)
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := testEngine(t)
			key := key1()
			seed(t, e, key, 10, 80)
			for previous := 0; previous < index; previous++ {
				// Each case independently builds the state it needs; this loop documents the fixed order.
				_ = previous
			}
			wantCode(t, tc.run(e, key), tc.code)
		})
	}
}

func TestErrorPriorityAdjacentPairwise(t *testing.T) {
	cases := []struct {
		name string
		want ErrorCode
		run  func(*Engine, RecordKey) error
	}{
		{"invalid beats clock", ErrInvalid, func(e *Engine, k RecordKey) error {
			return e.ApplyReview(-1, ActorID(k.Student), RecordKey{})
		}},
		{"clock beats missing", ErrClock, func(e *Engine, k RecordKey) error {
			return e.ApplyReview(0, "ghost", RecordKey{Student: "ghost", Course: "math", Semester: "s1"})
		}},
		{"missing beats locked", ErrNotFound, func(e *Engine, k RecordKey) error {
			_ = e.LockSemester(11, "locker", "s1")
			return e.ApplyReview(12, "ghost", RecordKey{Student: "ghost", Course: "math", Semester: "s1"})
		}},
		{"locked beats permission", ErrLocked, func(e *Engine, k RecordKey) error {
			_ = e.LockSemester(11, "locker", "s1")
			return e.ProposeChange(12, "intruder", k, 82)
		}},
		{"permission beats timeout", ErrPermission, func(e *Engine, k RecordKey) error {
			return e.ProposeChange(16, "intruder", k, 82)
		}},
		{"timeout beats state", ErrTimeout, func(e *Engine, k RecordKey) error {
			return e.ApplyReview(16, "alice", RecordKey{Student: "alice", Course: "math", Semester: "other"})
		}},
		{"state beats score", ErrState, func(e *Engine, k RecordKey) error {
			_ = e.ApplyReview(12, "alice", k)
			_ = e.ProposeChange(13, "teacher", k, 85)
			return e.ProposeChange(14, "teacher", k, 999)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := testEngine(t)
			key := key1()
			seed(t, e, key, 10, 80)
			missingOther := RecordKey{Student: "alice", Course: "math", Semester: "other"}
			seed(t, e, missingOther, 10, 80)
			wantCode(t, tc.run(e, key), tc.want)
		})
	}
}

func BenchmarkSnapshotIndependentOfOtherRecords(b *testing.B) {
	engine, err := NewEngine(testConfig(), map[ActorID]int{"teacher": 1, "boss": 2, "locker": 5, "sp1": 4, "sp2": 4})
	if err != nil {
		b.Fatal(err)
	}
	target := key1()
	if err := engine.EnterInitialScore(0, "teacher", target, 80); err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 4096; i++ {
		key := RecordKey{Student: StudentID("student-" + strconv.Itoa(i)), Course: "math", Semester: "s1"}
		if err := engine.EnterInitialScore(int64(i), "teacher", key, 80); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.SnapshotAt(target, 1, engine.Clock()); err != nil {
			b.Fatal(err)
		}
	}
}
