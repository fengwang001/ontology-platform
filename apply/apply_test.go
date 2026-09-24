package apply_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ontology/apply"
	"ontology/name"
	"ontology/undo"
)

// mixReqs 产生 6 步：链 2 步（e->f, d->e）+ 三元环 4 步（借 1 个临时名）。
var mixReqs = []name.Rename{
	{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"},
	{Old: "d", New: "e"}, {Old: "e", New: "f"},
}

const mixSteps = 6

func freshNS() *name.Namespace { return name.New("a", "b", "c", "d", "e") }

func TestExecuteRollback(t *testing.T) {
	for _, k := range []int{0, mixSteps / 2, mixSteps - 1} { // 首步 / 中间 / 末步
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			ns := freshNS()
			before := ns.Snapshot()
			j := filepath.Join(t.TempDir(), "j")
			if _, err := apply.Execute(ns, mixReqs, j, &apply.Options{FailAt: k}); err == nil {
				t.Fatal("expected injected failure")
			}
			if !slices.Equal(before, ns.Snapshot()) {
				t.Fatalf("after rollback ns = %v, want %v", ns.Snapshot(), before)
			}
			rep, err := undo.Undo(ns, j) // 回滚后日志已删，撤销应为无操作
			if err != nil || rep.Undone != 0 {
				t.Errorf("undo after rollback = %+v, %v", rep, err)
			}
			if !slices.Equal(before, ns.Snapshot()) {
				t.Error("namespace changed by no-op undo")
			}
		})
	}
}

func TestExecuteUndoIdentity(t *testing.T) {
	ns := freshNS()
	before := ns.Snapshot()
	j := filepath.Join(t.TempDir(), "j")
	p, err := apply.Execute(ns, mixReqs, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := undo.Undo(ns, j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Undone != len(p.Steps) || len(rep.Unrecoverable) != 0 {
		t.Errorf("report = %+v, want %d undone", rep, len(p.Steps))
	}
	if !slices.Equal(before, ns.Snapshot()) {
		t.Fatal("namespace not identical after undo")
	}
	rep2, err := undo.Undo(ns, j) // 重复撤销必须幂等
	if err != nil || rep2.Undone != 0 || len(rep2.Unrecoverable) != 0 {
		t.Errorf("second undo = %+v, %v", rep2, err)
	}
	if !slices.Equal(before, ns.Snapshot()) {
		t.Error("namespace changed by second undo")
	}
}

func TestTruncateClassifyAndRecover(t *testing.T) {
	dir := t.TempDir()
	j := filepath.Join(dir, "j")
	if _, err := apply.Execute(freshNS(), mixReqs, j, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(j)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[error]int{}
	for cut := 1; cut < len(data); cut++ { // 逐字节遍历全部截断点
		_, _, perr := undo.Parse(data[:cut])
		switch {
		case errors.Is(perr, undo.ErrHeaderIncomplete):
			seen[undo.ErrHeaderIncomplete]++
		case errors.Is(perr, undo.ErrRecordIncomplete):
			seen[undo.ErrRecordIncomplete]++
		case errors.Is(perr, undo.ErrCRCMismatch):
			seen[undo.ErrCRCMismatch]++
		default:
			t.Errorf("cut=%d: unclassified error %v", cut, perr)
		}

		ns := freshNS()
		jj := filepath.Join(dir, "cut")
		if _, err := apply.Execute(ns, mixReqs, jj, nil); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jj, data[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		rep, uerr := undo.Undo(ns, jj)
		if errors.Is(uerr, undo.ErrHeaderIncomplete) {
			if rep.Undone != 0 {
				t.Errorf("cut=%d: undid %d steps despite bad header", cut, rep.Undone)
			}
			continue
		}
		if rep.Undone+len(rep.Unrecoverable) != mixSteps {
			t.Errorf("cut=%d: undone %d + lost %v != %d steps", cut, rep.Undone, rep.Unrecoverable, mixSteps)
		}
	}
	for _, e := range []error{undo.ErrHeaderIncomplete, undo.ErrRecordIncomplete, undo.ErrCRCMismatch} {
		if seen[e] == 0 {
			t.Errorf("class %v never observed", e)
		}
	}
}

func TestConcurrentModifyBlocked(t *testing.T) {
	ns := name.New("a", "b")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := apply.Execute(ns, []name.Rename{{Old: "a", New: "x"}, {Old: "b", New: "y"}},
			filepath.Join(t.TempDir(), "j"), &apply.Options{FailAt: -1, Hook: func(i int) {
				if i == 0 {
					close(entered)
					<-release
				}
			}})
		done <- err
	}()
	<-entered
	added := make(chan bool, 1)
	go func() { added <- ns.Add("z") }()
	select {
	case <-added:
		t.Fatal("concurrent Add was not blocked during batch execution")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !<-added || !ns.Has("z") {
		t.Error("blocked Add should succeed after batch finished")
	}
}
