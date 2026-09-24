package sparse

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
)

func TestLookupThreePositions(t *testing.T) {
	// N=8，锚点序号 0,8,16,...
	x := New(8, []Anchor{{0, 32}, {8, 100}, {16, 200}})
	cases := []struct {
		name   string
		from   uint64
		wantS  uint64
		wantSk uint64
		empty  bool
	}{
		{"at anchor", 8, 8, 0, false},
		{"between", 10, 8, 2, false},
		{"before first", 0, 0, 0, false},
		{"negative-clamped", 0, 0, 0, false},
	}
	// 「小于第一个锚点」用一个首序号非 0 的索引单独断言。
	y := New(8, []Anchor{{100, 32}, {108, 100}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := x.Lookup(tc.from)
			if err != nil || a.Seq != tc.wantS {
				t.Fatalf("lookup(%d)=%+v,%v want seq %d", tc.from, a, err, tc.wantS)
			}
		})
	}
	if _, err := y.Lookup(50); !errors.Is(err, ErrEmpty) {
		t.Fatalf("before-first-anchor err=%v want ErrEmpty", err)
	}
}

func TestRebuildByteIdentical(t *testing.T) {
	dir := t.TempDir()
	w, err := segment.Create(dir, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		if _, err := w.Append(event.Event{Seq: uint64(i), Payload: []byte("payload")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := segment.Path(dir, 1)
	x1, err := Build(logPath, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := x1.Save(logPath); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(filepath.Join(dir, "seg-000001.idx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "seg-000001.idx")); err != nil {
		t.Fatal(err)
	}
	x2, err := Build(logPath, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := x2.Save(logPath); err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := os.ReadFile(filepath.Join(dir, "seg-000001.idx"))
	if !bytes.Equal(orig, rebuilt) {
		t.Fatal("rebuilt index differs byte-for-byte")
	}
	if got := len(x2.Anchors()); got != 13 {
		t.Fatalf("anchors=%d want 13", got)
	}
}
