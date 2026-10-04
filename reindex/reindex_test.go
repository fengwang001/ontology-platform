package reindex_test

import (
	"errors"
	"testing"

	"ontology/dest"
	"ontology/reindex"
	"ontology/source"
)

func wantErr(t *testing.T, err error, target error, name string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want error %v, got %v", name, target, err)
	}
}

func put(t *testing.T, c *reindex.Coordinator, id, body string, want int64) {
	t.Helper()
	seq, err := c.Put(id, body)
	if err != nil || seq != want {
		t.Fatalf("Put %s: seq=%d err=%v, want %d", id, seq, err, want)
	}
}

func del(t *testing.T, c *reindex.Coordinator, id string, want int64) {
	t.Helper()
	seq, err := c.Delete(id)
	if err != nil || seq != want {
		t.Fatalf("Delete %s: seq=%d err=%v, want %d", id, seq, err, want)
	}
}

func drain(c *reindex.Coordinator) {
	for {
		_, _, _, done, err := c.Step()
		if err != nil {
			panic(err)
		}
		if done {
			return
		}
	}
}

func TestDestTable(t *testing.T) {
	d, err := dest.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := d.Index("a", "1234", 1); !a {
		t.Fatal("a@1 should apply")
	}
	if a, _ := d.Index("a", "zz", 1); a {
		t.Fatal("equal ver must conflict")
	}
	if a, inc := d.Index("a", "toolong", 1); a || inc {
		t.Fatal("stale incompat must only conflict")
	}
	if a, inc := d.Index("a", "toolong", 2); !a || !inc {
		t.Fatal("newer incompat must tombstone")
	}
	if r := d.Records()["a"]; r.Alive || !r.Incompat || r.Ver != 2 || r.Body != "" {
		t.Fatalf("a should be incompat tombstone@2, got %+v", r)
	}
	if a, inc := d.Index("a", "ok", 3); !a || inc {
		t.Fatal("newer normal write should apply")
	}
	if len(d.Failed()) != 0 {
		t.Fatal("a must leave failed set")
	}
	if a := d.Delete("x", 5); !a {
		t.Fatal("delete missing id must leave tombstone")
	}
	if r := d.Records()["x"]; r.Alive || r.Ver != 5 {
		t.Fatalf("x should be tombstone@5, got %+v", r)
	}
	if a := d.Delete("x", 5); a {
		t.Fatal("equal ver against tombstone must conflict")
	}
	if a, _ := d.Index("x", "v", 5); a {
		t.Fatal("equal ver index against tombstone must conflict")
	}
	if d.Conflicts() != 4 {
		t.Fatalf("conflicts=%d want 4", d.Conflicts())
	}
	d.PurgeTombstones()
	if _, ok := d.Records()["x"]; ok {
		t.Fatal("tombstone x must be purged")
	}
	if _, e := dest.New(0); !errors.Is(e, dest.ErrInvalid) {
		t.Fatalf("L=0: %v", e)
	}
	if _, e := dest.New(65537); !errors.Is(e, dest.ErrInvalid) {
		t.Fatalf("L=65537: %v", e)
	}
}

func TestSourceTable(t *testing.T) {
	s := source.New()
	if _, e := s.Put("", "x"); !errors.Is(e, source.ErrInvalid) {
		t.Fatalf("empty id: %v", e)
	}
	long := make([]byte, 513)
	if _, e := s.Put(string(long), "x"); !errors.Is(e, source.ErrInvalid) {
		t.Fatalf("id too long: %v", e)
	}
	big := make([]byte, 65537)
	if _, e := s.Put("k", string(big)); !errors.Is(e, source.ErrInvalid) {
		t.Fatalf("body too long: %v", e)
	}
	if _, e := s.Delete("ghost"); !errors.Is(e, source.ErrNotFound) {
		t.Fatalf("delete missing: %v", e)
	}
	if seq, e := s.Put("a", "1"); e != nil || seq != 1 {
		t.Fatalf("put a: seq=%d err=%v", seq, e)
	}
	if seq, e := s.Put("b", "2"); e != nil || seq != 2 {
		t.Fatalf("put b: seq=%d err=%v", seq, e)
	}
	if seq, e := s.Delete("a"); e != nil || seq != 3 {
		t.Fatalf("delete a: seq=%d err=%v", seq, e)
	}
	if _, e := s.Delete("a"); !errors.Is(e, source.ErrNotFound) {
		t.Fatalf("delete a again: %v", e)
	}
	if s.Seq() != 3 {
		t.Fatal("rejected delete must not consume seq")
	}
	s.MarkSwitched()
	if _, e := s.Delete("ghost"); !errors.Is(e, source.ErrSwitched) {
		t.Fatalf("switched precedes notfound: %v", e)
	}
	if _, e := s.Put("a", "9"); !errors.Is(e, source.ErrSwitched) {
		t.Fatalf("put after switched: %v", e)
	}
}

func TestExampleFromSpec(t *testing.T) {
	c, err := reindex.New(4)
	if err != nil {
		t.Fatal(err)
	}
	put(t, c, "a", "1", 1)
	put(t, c, "b", "2", 2)
	put(t, c, "c", "3", 3)
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	del(t, c, "b", 4)
	put(t, c, "a", "9", 5)
	ap, cf, ic, done, err := c.Step()
	if err != nil || ap != 0 || cf != 2 || ic != 0 || done {
		t.Fatalf("step1 got (%d,%d,%d,%v,%v)", ap, cf, ic, done, err)
	}
	ap, cf, ic, done, err = c.Step()
	if err != nil || ap != 1 || cf != 0 || ic != 0 || !done {
		t.Fatalf("step2 got (%d,%d,%d,%v,%v)", ap, cf, ic, done, err)
	}
	ap, cf, ic, done, err = c.Step()
	if err != nil || ap != 0 || cf != 0 || ic != 0 || !done {
		t.Fatalf("step3 got (%d,%d,%d,%v,%v)", ap, cf, ic, done, err)
	}
	if err := c.Cutover(0); err != nil {
		t.Fatalf("cutover: %v", err)
	}
	rec := c.Dest().Records()
	if len(rec) != 2 {
		t.Fatalf("dest size=%d want 2", len(rec))
	}
	if r := rec["a"]; !r.Alive || r.Ver != 5 || r.Body != "9" {
		t.Fatalf("a=%+v", r)
	}
	if r := rec["c"]; !r.Alive || r.Ver != 3 || r.Body != "3" {
		t.Fatalf("c=%+v", r)
	}
	if _, e := c.Put("a", "x"); !errors.Is(e, source.ErrSwitched) {
		t.Fatalf("put after cutover: %v", e)
	}
	if _, e := c.Delete("a"); !errors.Is(e, source.ErrSwitched) {
		t.Fatalf("delete after cutover: %v", e)
	}
}

func TestIncompatInterleave(t *testing.T) {
	c, _ := reindex.New(4)
	put(t, c, "a", "1", 1)
	put(t, c, "b", "2", 2)
	put(t, c, "c", "3", 3)
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	del(t, c, "b", 4)
	put(t, c, "a", "9", 5)
	put(t, c, "c", "toolong", 6)
	if len(c.Dest().Failed()) != 1 {
		t.Fatal("failed set should be {c}")
	}
	var cf int
	for {
		_, c0, _, done, err := c.Step()
		if err != nil {
			t.Fatal(err)
		}
		cf += c0
		if done {
			break
		}
	}
	if cf != 3 {
		t.Fatalf("backfill conflicts=%d want 3", cf)
	}
	wantErr(t, c.Cutover(0), reindex.ErrTolerance, "tol 0")
	if err := c.Cutover(1); err != nil {
		t.Fatalf("tol 1 must pass: %v", err)
	}

	c2, _ := reindex.New(4)
	put(t, c2, "a", "1", 1)
	put(t, c2, "c", "3", 2)
	if err := c2.Start(1); err != nil {
		t.Fatal(err)
	}
	put(t, c2, "c", "toolong", 3)
	drain(c2)
	put(t, c2, "c", "ok", 4)
	if len(c2.Dest().Failed()) != 0 {
		t.Fatal("c newer write must clear failed set")
	}
	if err := c2.Cutover(0); err != nil {
		t.Fatalf("cutover after repair: %v", err)
	}
}

func TestSnapshotIncompat(t *testing.T) {
	c, _ := reindex.New(1)
	put(t, c, "a", "aa", 1)
	put(t, c, "b", "bb", 2)
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	put(t, c, "b", "z", 3)
	ap, cf, ic, done, err := c.Step()
	if err != nil {
		t.Fatal(err)
	}
	if ap != 0 || cf != 1 || ic != 1 || !done {
		t.Fatalf("got (%d,%d,%d,%v)", ap, cf, ic, done)
	}
	if _, ok := c.Dest().Failed()["a"]; !ok {
		t.Fatal("a must be incompat tombstone from backfill")
	}
	if r := c.Dest().Records()["b"]; !r.Alive || r.Ver != 3 || r.Body != "z" {
		t.Fatalf("b=%+v", r)
	}
}

func TestCutoverRejectionOrder(t *testing.T) {
	c, _ := reindex.New(4)
	wantErr(t, c.Cutover(-1), reindex.ErrInvalid, "Idle illegal")
	wantErr(t, c.Cutover(0), reindex.ErrState, "Idle state")
	put(t, c, "a", "1", 1)
	if err := c.Start(10); err != nil {
		t.Fatal(err)
	}
	wantErr(t, c.Cutover(1_000_001), reindex.ErrInvalid, "Running illegal")
	wantErr(t, c.Cutover(0), reindex.ErrNotFinished, "unfinished")
	c.Step()
	if err := c.Cutover(0); err != nil {
		t.Fatalf("equal tol passes: %v", err)
	}
	wantErr(t, c.Cutover(0), reindex.ErrState, "Switched")

	c2, _ := reindex.New(1)
	put(t, c2, "a", "aa", 1)
	put(t, c2, "b", "bb", 2)
	if err := c2.Start(10); err != nil {
		t.Fatal(err)
	}
	drain(c2)
	if len(c2.Dest().Failed()) != 2 {
		t.Fatal("want 2 failures")
	}
	wantErr(t, c2.Cutover(1), reindex.ErrTolerance, "tol one less")
	if err := c2.Cutover(2); err != nil {
		t.Fatalf("tol equal must pass: %v", err)
	}
}

func TestStartStepAbort(t *testing.T) {
	c, _ := reindex.New(4)
	wantErr(t, c.Start(0), reindex.ErrInvalid, "B=0")
	wantErr(t, c.Start(1001), reindex.ErrInvalid, "B=1001")
	wantErr(t, c.Abort(), reindex.ErrState, "abort idle")
	if _, _, _, _, e := c.Step(); !errors.Is(e, reindex.ErrState) {
		t.Fatalf("step idle: %v", e)
	}
	put(t, c, "a", "1", 1)
	if err := c.Start(2); err != nil {
		t.Fatalf("first start: %v", err)
	}
	wantErr(t, c.Start(2), reindex.ErrState, "start while running")
	if _, _, _, _, e := c.Step(); e != nil {
		t.Fatalf("step: %v", e)
	}
	if err := c.Abort(); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if c.State() != reindex.Idle || len(c.Dest().Records()) != 0 || c.Dest().Conflicts() != 0 {
		t.Fatal("abort must reset dest and state")
	}
	if err := c.Start(2); err != nil {
		t.Fatalf("restart after abort: %v", err)
	}
	wantErr(t, c.Start(2), reindex.ErrState, "start running 2")
	if err := c.Abort(); err != nil {
		t.Fatal(err)
	}
	// dest 非空（直接写内部目标）也属状态不符：先 Start 制造记录，Abort 后不适用；
	// 这里通过 Running 双写后 Abort 再手工验证空 Start 正常，非空情形见下。
	d := c.Dest()
	d.Index("z", "z", 1)
	wantErr(t, c.Start(2), reindex.ErrState, "start with nonempty dest")
}
