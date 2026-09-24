package segment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func TestWriteRead(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	in := []event.Event{
		{Seq: 10, Payload: nil},
		{Seq: 11, Payload: []byte{}},
		{Seq: 12, Payload: []byte("payload-x")},
	}
	for _, e := range in {
		if _, err := w.Append(e); err != nil {
			t.Fatalf("append %d: %v", e.Seq, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(Path(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if h := r.Header(); h.FirstSeq != 10 || h.Count != 3 {
		t.Fatalf("header=%+v", h)
	}
	for _, want := range in {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		if got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	}
	if err := Inspect(Path(dir, 1)); err != nil {
		t.Fatalf("inspect healthy: %v", err)
	}
}

func TestAppendSeqAndList(t *testing.T) {
	dir := t.TempDir()
	w, _ := Create(dir, 2, 5)
	defer w.Close()
	cases := []struct {
		ev  event.Event
		err bool
	}{
		{event.Event{Seq: 5}, false},
		{event.Event{Seq: 7}, true},
		{event.Event{Seq: 6}, false},
		{event.Event{Seq: 6}, true},
	}
	for _, tc := range cases {
		_, err := w.Append(tc.ev)
		if (err != nil) != tc.err {
			t.Fatalf("seq=%d err=%v wantErr=%v", tc.ev.Seq, err, tc.err)
		}
	}
	paths, err := List(dir)
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != "seg-000002.log" {
		t.Fatalf("list=%v err=%v", paths, err)
	}
	if got := IndexPath(paths[0]); filepath.Base(got) != "seg-000002.idx" {
		t.Fatalf("index path=%s", got)
	}
}

func TestInspectClasses(t *testing.T) {
	dir := t.TempDir()
	path := buildSegment(t, dir, 3)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		cut  int
		want error
	}{
		{"header", HeaderSize - 1, ErrHeaderIncomplete},
		{"len prefix", HeaderSize + 2, ErrLenPrefixIncomplete},
		{"body", HeaderSize + 6, ErrBodyIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name+".log")
			if err := os.WriteFile(p, good[:tc.cut], 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Inspect(p); !errors.Is(err, tc.want) {
				t.Fatalf("inspect err=%v want %v", err, tc.want)
			}
		})
	}
}

func buildSegment(t *testing.T, dir string, n int) string {
	t.Helper()
	w, err := Create(filepath.Join(dir, "x"), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := w.Append(event.Event{Seq: uint64(i), Payload: []byte("p")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return Path(filepath.Join(dir, "x"), 1)
}
