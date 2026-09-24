package apply_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

var threeSteps = []plan.Step{{Old: "a", New: "x"}, {Old: "b", New: "y"}, {Old: "c", New: "z"}}

func freshSpace() *name.Space { return name.Must("a", "b", "c") }

func TestExecuteThenUndo(t *testing.T) {
	s := freshSpace()
	before := s.Snapshot()
	log := filepath.Join(t.TempDir(), "op.log")
	if err := (&apply.Executor{LogPath: log, FailAt: -1}).Do(s, threeSteps); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if s.Equal(freshSpace()) {
		t.Fatal("execution had no effect")
	}
	res, err := undo.Undo(s, log)
	if err != nil || res.Undone != 3 || len(res.Lost) != 0 {
		t.Fatalf("Undo: %v %+v", err, res)
	}
	if got := s.Snapshot(); !equalStrs(got, before) {
		t.Fatalf("after undo = %v, want %v", got, before)
	}
	res2, err := undo.Undo(s, log)
	if err != nil || res2.Undone != 0 {
		t.Fatalf("second Undo: %v %+v", err, res2)
	}
	if got := s.Snapshot(); !equalStrs(got, before) {
		t.Fatal("second Undo mutated the space")
	}
}

func TestFailureRollback(t *testing.T) {
	for _, k := range []int{0, 1, 2} { // 首步 / 中间 / 最后一步
		s := freshSpace()
		before := s.Snapshot()
		err := (&apply.Executor{FailAt: k}).Do(s, threeSteps)
		if !errors.Is(err, apply.ErrInjected) {
			t.Fatalf("k=%d: err = %v", k, err)
		}
		if got := s.Snapshot(); !equalStrs(got, before) {
			t.Fatalf("k=%d: rollback incomplete: %v", k, got)
		}
	}
}

func TestTruncation(t *testing.T) {
	full := apply.Encode(threeSteps)
	L := len(full) // 16 头 + 18 记录 + 4 CRC = 38
	bodyEnd := apply.HeaderSize + 18
	dir := t.TempDir()
	counts := map[string]int{}
	for cut := 1; cut <= L-1; cut++ {
		s := freshSpace()
		log := filepath.Join(dir, "op.log")
		if err := (&apply.Executor{LogPath: log, FailAt: -1}).Do(s, threeSteps); err != nil {
			t.Fatalf("cut=%d Do: %v", cut, err)
		}
		if err := os.WriteFile(log, full[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		os.Remove(log + ".done")
		res, err := undo.Undo(s, log)
		var class string
		switch {
		case errors.Is(err, undo.ErrHeaderIncomplete):
			class = "header"
		case errors.Is(err, undo.ErrRecordIncomplete):
			class = "record"
		case errors.Is(err, undo.ErrCRCMismatch):
			class = "crc"
		default:
			t.Fatalf("cut=%d: unclassified err %v", cut, err)
		}
		want := "record"
		if cut < apply.HeaderSize {
			want = "header"
		} else if cut >= bodyEnd {
			want = "crc"
		}
		if class != want {
			t.Fatalf("cut=%d: class=%s want %s", cut, class, want)
		}
		counts[class]++
		switch class {
		case "header":
			if res != nil {
				t.Fatalf("cut=%d: header cut should recover nothing", cut)
			}
		case "record":
			recovered := (cut - apply.HeaderSize) / 6
			if res.Undone != recovered || len(res.Lost) != 3-recovered {
				t.Fatalf("cut=%d: undone=%d lost=%v", cut, res.Undone, res.Lost)
			}
		case "crc":
			if res.Undone != 3 || !s.Equal(freshSpace()) {
				t.Fatalf("cut=%d: crc cut should fully undo", cut)
			}
		}
	}
	if counts["header"] == 0 || counts["record"] == 0 || counts["crc"] == 0 {
		t.Fatalf("missing class coverage: %v", counts)
	}
}

func TestConcurrentModifyBlocked(t *testing.T) {
	s := freshSpace()
	entered := make(chan struct{})
	release := make(chan struct{})
	ex := &apply.Executor{FailAt: -1, Hook: func(i int) {
		if i == 1 {
			close(entered)
			<-release
		}
	}}
	done := make(chan error, 1)
	go func() { done <- ex.Do(s, threeSteps) }()
	<-entered
	added := make(chan error, 1)
	go func() { added <- s.Add("intruder") }()
	select {
	case <-added:
		t.Fatal("concurrent Add was not blocked during batch")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Do: %v", err)
	}
	if err := <-added; err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !s.Has("intruder") {
		t.Fatal("blocked Add never landed")
	}
}

func equalStrs(a, b []string) bool {
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
