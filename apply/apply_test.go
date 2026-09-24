package apply

import (
	"errors"
	"os"
	"testing"

	"ontology/name"
	"ontology/plan"
)

func TestExecuteAndRollbackTable(t *testing.T) {
	steps := []plan.Step{{From: "c", To: "z"}, {From: "b", To: "c"}, {From: "a", To: "b"}}
	tests := []struct {
		name    string
		failAt  int
		wantErr error
		applied int
		undone  int
		final   []string
	}{
		{"success", 0, nil, 3, 0, []string{"z", "c", "b"}},
		{"fail-first", 1, ErrExecutionFailed, 0, 0, []string{"a", "b", "c"}},
		{"fail-middle", 2, ErrExecutionFailed, 1, 1, []string{"a", "b", "c"}},
		{"fail-last", 3, ErrExecutionFailed, 2, 2, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ns := name.New("a", "b", "c")
			res, err := Applier{FailAt: tt.failAt}.Execute(ns, steps, dir)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !name.Equal(ns, name.New(tt.final...)) {
				t.Fatalf("final = %#v, want %#v", ns.Snapshot(), tt.final)
			}
			if tt.wantErr == nil {
				_, statErr := os.Stat(res.LogPath)
				if res.Applied != tt.applied || statErr != nil {
					t.Fatalf("result=%#v stat=%v", res, statErr)
				}
				return
			}
			entries, _ := os.ReadDir(dir)
			if res != nil || len(entries) != 0 {
				t.Fatalf("res=%#v entries=%d", res, len(entries))
			}
		})
	}
}
