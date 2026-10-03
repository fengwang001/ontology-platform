package runner

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/ledger"
	"ontology/script"
)

func noop(int64) error { return nil }

func registerScripts(t *testing.T, e *Engine, scripts ...script.Script) {
	t.Helper()
	for _, s := range scripts {
		if err := e.Register(1, s); err != nil {
			t.Fatalf("Register(%+v): %v", s, err)
		}
	}
}

func scriptVer(ver int64) script.Script {
	return script.Script{Ver: ver, Sum: uint64(ver * 100), HasUndo: true}
}

func TestOutOfOrderAllowedAndStrict(t *testing.T) {
	for _, tc := range []struct {
		name string
		ooo  bool
		err  error
	}{
		{name: "allowed", ooo: true},
		{name: "strict", ooo: false, err: ErrOutOfOrder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(tc.ooo, 10, noop, noop)
			if err != nil {
				t.Fatal(err)
			}
			registerScripts(t, e, scriptVer(1), scriptVer(3))
			if _, err := e.Migrate(1, 1); err != nil {
				t.Fatal(err)
			}
			registerScripts(t, e, scriptVer(2))
			_, err = e.Migrate(1, 2)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err == nil {
				rows := e.Ledger()
				if rows[2].Ver != 2 || rows[2].Status != ledger.StatusSuccess {
					t.Fatalf("rows = %+v", rows)
				}
			} else if ver, _ := err.(*VersionError); ver.Ver != 2 {
				t.Fatalf("error ver = %d, want 2", ver.Ver)
			}
		})
	}
}

func TestMaximumAppliedOnlyUsesSuccessRows(t *testing.T) {
	e, err := New(false, 10, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(2))
	if _, err := e.Migrate(1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Undo(2, 2, 0); err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(5))
	result, err := e.Migrate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Done, []int64{2, 5}) {
		t.Fatalf("done = %v", result.Done)
	}
}

func TestChecksumBeforeOutOfOrder(t *testing.T) {
	e, err := New(false, 10, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(3))
	if _, err := e.Migrate(1, 1); err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, script.Script{Ver: 1, Sum: 101, HasUndo: true}, scriptVer(2))
	_, err = e.Migrate(1, 2)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if ver, _ := err.(*VersionError); ver.Ver != 1 {
		t.Fatalf("error ver = %d, want 1", ver.Ver)
	}
}

func TestLimitExactMoreFalse(t *testing.T) {
	e, err := New(false, 2, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(2))
	result, err := e.Migrate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Done, []int64{1, 2}) || result.More || result.Fail != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestLimitAcrossBatches(t *testing.T) {
	e, err := New(false, 2, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(2), scriptVer(3), scriptVer(4), scriptVer(5))
	expected := []struct {
		done []int64
		more bool
	}{
		{[]int64{1, 2}, true},
		{[]int64{3, 4}, true},
		{[]int64{5}, false},
	}
	for i, want := range expected {
		got, err := e.Migrate(1, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Done, want.done) || got.More != want.more {
			t.Fatalf("batch %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestFailedBlocksAndRepairDoesNotReuseRank(t *testing.T) {
	var failedV2 bool
	e, err := New(true, 10, func(ver int64) error {
		if ver == 2 && !failedV2 {
			failedV2 = true
			return errors.New("boom")
		}
		return nil
	}, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(2), scriptVer(3))
	result, err := e.Migrate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Done, []int64{1}) || result.Fail != 2 {
		t.Fatalf("result = %+v", result)
	}
	_, err = e.Migrate(1, 2)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	removed, err := e.Repair(2, 3)
	if err != nil || removed != 1 {
		t.Fatalf("Repair = %d, %v", removed, err)
	}
	result, err = e.Migrate(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Done, []int64{2, 3}) {
		t.Fatalf("done = %v", result.Done)
	}
	rows := e.Ledger()
	if rows[1].Status != ledger.StatusSuccess || rows[1].Rank != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUndoToKeepsBoundaryAndUsesRankOrder(t *testing.T) {
	var order []int64
	e, err := New(true, 10, noop, func(ver int64) error {
		order = append(order, ver)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(3))
	if _, err := e.Migrate(1, 1); err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(2))
	if _, err := e.Migrate(1, 2); err != nil {
		t.Fatal(err)
	}
	result, err := e.Undo(2, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Undone, []int64{2, 3}) || !reflect.DeepEqual(order, []int64{2, 3}) {
		t.Fatalf("undo = %+v order = %v", result, order)
	}
	rows := e.Ledger()
	if rows[0].Status != ledger.StatusSuccess || rows[1].Status != ledger.StatusUndone || rows[2].Status != ledger.StatusUndone {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUndoFailureStateAndNoUndoReportsMax(t *testing.T) {
	e, err := New(true, 10, noop, func(ver int64) error {
		if ver == 3 {
			return errors.New("undo boom")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1), scriptVer(3))
	if _, err := e.Migrate(1, 1); err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(2))
	if _, err := e.Migrate(1, 2); err != nil {
		t.Fatal(err)
	}
	result, err := e.Undo(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Undone, []int64{2}) || result.Fail != 3 {
		t.Fatalf("result = %+v", result)
	}
	rows := e.Ledger()
	if rows[2].Status != ledger.StatusUndone || rows[1].Status != ledger.StatusSuccess || rows[len(rows)-1].Status != ledger.StatusFailed {
		t.Fatalf("rows = %+v", rows)
	}

	e2, err := New(false, 10, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e2,
		script.Script{Ver: 2, Sum: 200, HasUndo: true},
		script.Script{Ver: 3, Sum: 300},
	)
	if _, err := e2.Migrate(1, 1); err != nil {
		t.Fatal(err)
	}
	_, err = e2.Undo(2, 2, 1)
	if !errors.Is(err, ErrNoUndo) {
		t.Fatalf("err = %v, want ErrNoUndo", err)
	}
	if ver, _ := err.(*VersionError); ver.Ver != 3 {
		t.Fatalf("error ver = %d, want 3", ver.Ver)
	}
}

func TestPanicTreatedAsFailure(t *testing.T) {
	e, err := New(false, 10, func(ver int64) error {
		if ver == 1 {
			panic("exec panic")
		}
		return nil
	}, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(1))
	result, err := e.Migrate(1, 1)
	if err != nil || result.Fail != 1 {
		t.Fatalf("Migrate = %+v, %v", result, err)
	}
	if rows := e.Ledger(); rows[0].Status != ledger.StatusFailed {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRejectionDoesNotChangeStateOrNow(t *testing.T) {
	e, err := New(false, 10, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	registerScripts(t, e, scriptVer(2))
	_, err = e.Migrate(0, 1)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Undo(2, 1, 5); err != nil {
		t.Fatal(err)
	}
	before := e.Ledger()
	_, err = e.Migrate(1, 0)
	if !errors.Is(err, ErrNow) {
		t.Fatalf("err = %v", err)
	}
	registerScripts(t, e, scriptVer(1))
	_, err = e.Migrate(1, 1)
	if err != nil {
		t.Fatalf("accepted after rejected calls: %v", err)
	}
	if after := e.Ledger(); len(before) != 0 || len(after) != 2 {
		t.Fatalf("before %d after %d", len(before), len(after))
	}
}

func TestConcurrentCallsAreSafe(t *testing.T) {
	e, err := New(true, 1000, noop, noop)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 20; i++ {
		wg.Add(3)
		go func(ver int64) {
			defer wg.Done()
			_ = e.Register(1, scriptVer(ver))
		}(i)
		go func(now int64) {
			defer wg.Done()
			_, _ = e.Migrate(1, now)
		}(i)
		go func() {
			defer wg.Done()
			_ = e.Ledger()
			_ = e.Registered()
		}()
	}
	wg.Wait()
}
