package kvlog

import (
	"testing"
)

func TestSmokePutGetDelete(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 40, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("alpha"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("beta"), []byte("22")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("alpha"), []byte("111")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Delete([]byte("beta")); err != nil {
		t.Fatal(err)
	}
	r, err := e.Get([]byte("alpha"))
	if err != nil || r.Status != StatusPresent || string(r.Value) != "111" {
		t.Fatalf("alpha = %+v %v", r, err)
	}
	r, err = e.Get([]byte("beta"))
	if err != nil || r.Status != StatusDeleted {
		t.Fatalf("beta = %+v %v", r, err)
	}
	r, err = e.Get([]byte("nope"))
	if err != nil || r.Status != StatusMissing {
		t.Fatalf("nope = %+v %v", r, err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := Open(dir, Config{MaxSegmentBytes: 40, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	r, err = e2.Get([]byte("alpha"))
	if err != nil || r.Status != StatusPresent || string(r.Value) != "111" {
		t.Fatalf("reopen alpha = %+v %v", r, err)
	}
	r, _ = e2.Get([]byte("beta"))
	if r.Status != StatusDeleted {
		t.Fatalf("reopen beta = %v", r.Status)
	}
	if e2.NextSeq() != 5 {
		t.Fatalf("nextSeq=%d", e2.NextSeq())
	}
	t.Logf("segments after reopen=%d", len(e2.segs))
}
