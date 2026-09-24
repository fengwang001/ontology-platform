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

func buildLog(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	ns := name.New("a", "b", "c")
	steps := []plan.Step{{From: "c", To: "z"}, {From: "b", To: "c"}, {From: "a", To: "b"}}
	res, err := apply.Applier{}.Execute(ns, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEveryTruncationPointTable(t *testing.T) {
	data := buildLog(t)
	first := nextBoundary(data, 0)
	second := nextBoundary(data, first)
	third := nextBoundary(data, second)
	if first <= 0 || second <= first || third <= second || third != len(data) {
		t.Fatalf("invalid boundaries: %d %d %d len=%d", first, second, third, len(data))
	}
	tests := []struct {
		name      string
		cut       int
		err       error
		recovered int
	}{
		{"first-header", 1, ErrHeaderTruncated, 0},
		{"first-record", first - 1, ErrRecordTruncated, 0},
		{"first-crc", first, ErrCRCMismatch, 0},
		{"second-header", first + 1, ErrHeaderTruncated, 1},
		{"second-record", second - 1, ErrRecordTruncated, 1},
		{"second-crc", second, ErrCRCMismatch, 1},
		{"third-header", second + 1, ErrHeaderTruncated, 2},
		{"third-record", third - 1, ErrRecordTruncated, 2},
		{"third-crc", third, nil, 3},
	}
	classCounts := map[error]int{}
	for cut := 1; cut < len(data); cut++ {
		steps, res, err := Parse(data[:cut])
		classCounts[err]++
		switch {
		case cut < first:
			want := ErrHeaderTruncated
			if cut >= 18 {
				want = ErrRecordTruncated
			}
			if !errors.Is(err, want) || res.Recovered != 0 {
				t.Fatalf("cut %d: %v", cut, err)
			}
		case cut < second:
			want := ErrCRCMismatch
			if cut > first+17 {
				want = ErrRecordTruncated
			}
			if cut < first {
				want = ErrHeaderTruncated
			}
			if !errors.Is(err, want) || res.Recovered != countRecovered(cut, first, second, third) {
				t.Fatalf("cut %d: err=%v res=%+v", cut, err, res)
			}
		}
		if cut == len(data)-1 && !errors.Is(err, ErrCRCMismatch) {
			t.Fatalf("last cut: %v", err)
		}
		_ = steps
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, res, err := Parse(data[:tt.cut])
			if !errors.Is(err, tt.err) || res.Recovered != tt.recovered {
				t.Fatalf("err=%v recovered=%d", err, res.Recovered)
			}
		})
	}
	for _, kind := range []error{ErrHeaderTruncated, ErrRecordTruncated, ErrCRCMismatch} {
		if classCounts[kind] == 0 {
			t.Fatalf("missing class %v", kind)
		}
	}
}

func TestUndoIdempotent(t *testing.T) {
	dir := t.TempDir()
	ns := name.New("a", "b", "c")
	steps := []plan.Step{{From: "c", To: "z"}, {From: "b", To: "c"}, {From: "a", To: "b"}}
	res, err := apply.Applier{}.Execute(ns, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		got, err := File(ns, res.LogPath)
		if err != nil || !name.Equal(ns, name.New("a", "b", "c")) {
			t.Fatalf("attempt %d: err=%v state=%#v", attempt, err, ns.Snapshot())
		}
		if attempt == 1 && got.Undone != 0 {
			t.Fatalf("second undo changed %d steps", got.Undone)
		}
	}
}

func TestTruncatedUndoUsesRecoverablePrefix(t *testing.T) {
	data := buildLog(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.log")
	second := nextBoundary(data, nextBoundary(data, 0))
	if err := os.WriteFile(path, data[:second], 0o600); err != nil {
		t.Fatal(err)
	}
	ns := name.New("a", "b", "c")
	res, err := File(ns, path)
	if !errors.Is(err, ErrCRCMismatch) || res.Recovered != 1 || res.Undone != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func nextBoundary(data []byte, start int) int {
	for i := start + 18; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return len(data)
}

func countRecovered(cut, first, second, third int) int {
	switch {
	case cut >= third:
		return 3
	case cut >= second:
		return 2
	case cut >= first:
		return 1
	default:
		return 0
	}
}
