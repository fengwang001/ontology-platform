package undo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
)

func runBatch(t *testing.T, ns *name.Namespace, reqs []plan.Request, log string) *plan.Plan {
	t.Helper()
	ex := &apply.Executor{NS: ns, LogPath: log}
	p, err := ex.Run(reqs)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUndoCompleteAndIdempotent(t *testing.T) {
	cases := []struct {
		name string
		init []string
		reqs []plan.Request
	}{
		{"链", []string{"a", "b", "x"}, []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "x", New: "y"}}},
		{"三元环", []string{"a", "b", "c"}, []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}}},
		{"空批次", []string{"a"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init...)
			before := ns.Snapshot()
			log := filepath.Join(t.TempDir(), "u.log")
			p := runBatch(t, ns, tc.reqs, log)
			res, err := Undo(ns, log)
			if err != nil || res.Undone != len(p.Steps) || res.Remaining != 0 {
				t.Fatalf("res=%+v err=%v, 步数=%d", res, err, len(p.Steps))
			}
			if !name.Equal(before, ns.Snapshot()) {
				t.Fatal("撤销后命名空间与执行前不一致")
			}
			res2, err := Undo(ns, log)
			if err != nil || res2.Undone != 0 {
				t.Fatalf("重复撤销应为无操作: %+v err=%v", res2, err)
			}
			if !name.Equal(before, ns.Snapshot()) {
				t.Fatal("重复撤销改变了命名空间")
			}
		})
	}
}

func TestUndoTruncation(t *testing.T) {
	init := []string{"a", "b", "x"}
	reqs := []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "x", New: "y"}}
	full := apply.EncodeLog([]plan.Step{
		{Old: "b", New: "c"}, {Old: "a", New: "b"}, {Old: "x", New: "y"},
	})
	endOfRecords := len(full) - apply.FooterLen
	seen := map[error]int{}
	for cut := 1; cut < len(full); cut++ {
		var want error
		switch {
		case cut < apply.HeaderLen:
			want = ErrBadHeader
		case cut <= endOfRecords:
			want = ErrTruncated
		default:
			want = ErrCRCMismatch
		}
		recs, total, err := Parse(full[:cut])
		if !errors.Is(err, want) {
			t.Fatalf("截断 %d 字节: err=%v, want %v", cut, err, want)
		}
		seen[want]++
		if cut < apply.HeaderLen {
			continue
		}
		// 最大可恢复前缀：撤销后应等价于只执行了未恢复的后续步骤。
		ns := name.New(init...)
		runBatch(t, ns, reqs, "")
		path := filepath.Join(t.TempDir(), "t.log")
		if err := os.WriteFile(path, full[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		res, uerr := Undo(ns, path)
		if !errors.Is(uerr, want) || res.Recovered != len(recs) {
			t.Fatalf("截断 %d 字节: res=%+v uerr=%v", cut, res, uerr)
		}
		steps := []plan.Step{{Old: "b", New: "c"}, {Old: "a", New: "b"}, {Old: "x", New: "y"}}
		wantState := map[string]bool{"a": true, "b": true, "x": true}
		for _, s := range steps {
			delete(wantState, s.Old)
			wantState[s.New] = true
		}
		wantUndone := 0
		for i := len(recs) - 1; i >= 0; i-- {
			r := recs[i]
			if wantState[r.New] && !wantState[r.Old] {
				delete(wantState, r.New)
				wantState[r.Old] = true
				wantUndone++
			}
		}
		if res.Undone != wantUndone || res.Remaining != total-wantUndone {
			t.Fatalf("截断 %d 字节: Undone=%d Remaining=%d, want %d/%d",
				cut, res.Undone, res.Remaining, wantUndone, total-wantUndone)
		}
		if !name.Equal(wantState, ns.Snapshot()) {
			t.Fatalf("截断 %d 字节: 撤销后状态错误 %v", cut, ns.Snapshot())
		}
	}
	for _, e := range []error{ErrBadHeader, ErrTruncated, ErrCRCMismatch} {
		if seen[e] == 0 {
			t.Fatalf("截断点未覆盖错误类别 %v", e)
		}
	}
}
