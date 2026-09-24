package apply_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

func pipeline(t *testing.T, ns *name.Set, reqs []plan.Request) []plan.Step {
	t.Helper()
	if err := plan.Check(ns, reqs); err != nil {
		t.Fatal(err)
	}
	ord, cyc := plan.Order(reqs)
	cs, _, err := cycle.Break(ns, reqs, cyc)
	if err != nil {
		t.Fatal(err)
	}
	return append(ord, cs...)
}

func TestUndoRestores(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		reqs  []plan.Request
	}{
		{"chain", []string{"a", "b"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}}},
		{"two cycle", []string{"a", "b"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "a"}}},
		{"three cycle", []string{"a", "b", "c"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}}},
		{"mixed", []string{"a", "b", "p", "q", "s"}, []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "p", To: "q"}, {From: "q", To: "p"}, {From: "s", To: "t"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.names...)
			before := name.New(tc.names...)
			steps := pipeline(t, ns, tc.reqs)
			log := filepath.Join(t.TempDir(), "batch.log")
			if err := (&apply.Executor{NS: ns}).Run(steps, log); err != nil {
				t.Fatal(err)
			}
			unrec, err := undo.Undo(ns, log)
			if err != nil || len(unrec) != 0 {
				t.Fatalf("undo: unrecovered=%v err=%v", unrec, err)
			}
			if !ns.Equal(before) {
				t.Fatalf("namespace %v differs from before %v", ns.Snapshot(), before.Snapshot())
			}
			if _, err := os.Stat(log + ".done"); err != nil {
				t.Fatalf("log not marked done: %v", err)
			}
			unrec2, err2 := undo.Undo(ns, log)
			if err2 != nil || len(unrec2) != 0 || !ns.Equal(before) {
				t.Fatalf("second undo not a no-op: %v %v", unrec2, err2)
			}
		})
	}
}

func TestRollback(t *testing.T) {
	reqs := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"}, {From: "d", To: "e"}, {From: "e", To: "f"}}
	for _, k := range []int{1, 3, 5} { // first, middle, last step
		ns := name.New("a", "b", "c", "d", "e")
		before := name.New("a", "b", "c", "d", "e")
		steps := pipeline(t, ns, reqs)
		log := filepath.Join(t.TempDir(), "fail.log")
		ex := &apply.Executor{NS: ns, Hook: func(i int, _ plan.Step) error {
			if i == k-1 {
				return errors.New("injected")
			}
			return nil
		}}
		err := ex.Run(steps, log)
		if !errors.Is(err, apply.ErrStepFailed) {
			t.Fatalf("k=%d: err = %v, want ErrStepFailed", k, err)
		}
		if !ns.Equal(before) {
			t.Fatalf("k=%d: namespace %v not rolled back to %v", k, ns.Snapshot(), before.Snapshot())
		}
		if _, serr := os.Stat(log); !os.IsNotExist(serr) {
			t.Fatalf("k=%d: log should be removed after rollback", k)
		}
	}
}

func TestTruncateEveryByte(t *testing.T) {
	steps := []plan.Step{{From: "a", To: "x"}, {From: "b", To: "y"}, {From: "c", To: "z"}}
	data := apply.EncodeLog(steps)
	headerLen := bytes.IndexByte(data, '\n') + 1
	seen := map[error]bool{}
	for l := 1; l < len(data); l++ {
		got, total, err := apply.ParseLog(data[:l])
		switch {
		case l < headerLen:
			if !errors.Is(err, apply.ErrHeaderIncomplete) || total != -1 {
				t.Fatalf("L=%d: got total=%d err=%v, want header incomplete", l, total, err)
			}
		case data[l-1] != '\n':
			if !errors.Is(err, apply.ErrRecordIncomplete) {
				t.Fatalf("L=%d: got err=%v, want record incomplete", l, err)
			}
		default:
			if !errors.Is(err, apply.ErrCRCMismatch) {
				t.Fatalf("L=%d: got err=%v, want CRC mismatch", l, err)
			}
		}
		if l >= headerLen {
			wantRec := (l - headerLen) / 6 // each record line is 6 bytes
			if len(got) != wantRec {
				t.Fatalf("L=%d: recovered %d steps, want %d", l, len(got), wantRec)
			}
		}
		seen[err] = true
	}
	for _, e := range []error{apply.ErrHeaderIncomplete, apply.ErrRecordIncomplete, apply.ErrCRCMismatch} {
		if !seen[e] {
			t.Fatalf("class %v never produced", e)
		}
	}
}

func TestUndoTruncated(t *testing.T) {
	steps := []plan.Step{{From: "a", To: "x"}, {From: "b", To: "y"}, {From: "c", To: "z"}}
	data := apply.EncodeLog(steps)
	headerLen := bytes.IndexByte(data, '\n') + 1
	cases := []struct {
		name    string
		cut     int
		wantErr error
		unrec   []int
		undone  []string // names expected back after partial undo
	}{
		{"header cut", 5, apply.ErrHeaderIncomplete, nil, nil},
		{"mid record", headerLen + 6 + 3, apply.ErrRecordIncomplete, []int{1, 2}, []string{"a"}},
		{"record boundary", headerLen + 6, apply.ErrCRCMismatch, []int{1, 2}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New("a", "b", "c")
			log := filepath.Join(t.TempDir(), "trunc.log")
			if err := (&apply.Executor{NS: ns}).Run(steps, log); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(log, data[:tc.cut], 0o600); err != nil {
				t.Fatal(err)
			}
			unrec, err := undo.Undo(ns, log)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(unrec, tc.unrec) {
				t.Fatalf("unrecovered = %v, want %v", unrec, tc.unrec)
			}
			for _, n := range tc.undone {
				if !ns.Contains(n) {
					t.Fatalf("name %q should be restored", n)
				}
			}
		})
	}
}

func TestConcurrentBlocked(t *testing.T) {
	ns := name.New("a", "b")
	started := make(chan struct{})
	release := make(chan struct{})
	ex := &apply.Executor{NS: ns, Hook: func(int, plan.Step) error {
		close(started)
		<-release
		return nil
	}}
	runDone := make(chan error, 1)
	go func() { runDone <- ex.Run([]plan.Step{{From: "a", To: "c"}}, filepath.Join(t.TempDir(), "c.log")) }()
	<-started
	renamed := make(chan struct{})
	go func() { _ = ns.Rename("b", "d"); close(renamed) }()
	select {
	case <-renamed:
		t.Fatal("concurrent rename was not blocked during batch")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	<-renamed
	if !ns.Contains("d") {
		t.Fatal("blocked rename should complete after batch")
	}
}
