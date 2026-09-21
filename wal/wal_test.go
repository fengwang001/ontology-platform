package wal

import (
	"errors"
	"sync"
	"testing"
)

type memSink struct {
	fail  bool
	short bool
	buf   []byte
}

func (m *memSink) Write(p []byte) (int, error) {
	if m.fail {
		return 0, errors.New("injected disk failure")
	}
	if m.short {
		return len(p) / 2, nil
	}
	m.buf = append(m.buf, p...)
	return len(p), nil
}

func TestAppendAssignsSequentialSeq(t *testing.T) {
	l := New(&memSink{})
	if err := l.Append(Record{Txn: 1}, Record{Txn: 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{Txn: 2}); err != nil {
		t.Fatal(err)
	}
	recs, err := l.Replay(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("record %d has Seq %d", i, r.Seq)
		}
	}
	if l.LastSeq() != 3 {
		t.Fatalf("LastSeq = %d, want 3", l.LastSeq())
	}
}

func TestAppendFailureIsAtomicAndBurnsNoSeq(t *testing.T) {
	s := &memSink{fail: true}
	l := New(s)
	if err := l.Append(Record{Txn: 1}, Record{Txn: 1}); err == nil {
		t.Fatal("expected error from failing sink")
	}
	if l.LastSeq() != 0 {
		t.Fatalf("failed Append consumed Seq: LastSeq = %d", l.LastSeq())
	}
	recs, _ := l.Replay(0)
	if len(recs) != 0 {
		t.Fatalf("failed Append left %d records", len(recs))
	}
	s.fail = false
	if err := l.Append(Record{Txn: 2}); err != nil {
		t.Fatal(err)
	}
	if l.LastSeq() != 1 {
		t.Fatalf("Seq not reused after failure: LastSeq = %d", l.LastSeq())
	}
}

func TestAppendShortWriteFails(t *testing.T) {
	l := New(&memSink{short: true})
	if err := l.Append(Record{Txn: 1}); err == nil {
		t.Fatal("expected error on short write")
	}
	if l.LastSeq() != 0 {
		t.Fatal("short write consumed Seq")
	}
}

func TestReplayFromAndTruncate(t *testing.T) {
	l := New(&memSink{})
	for txn := uint64(1); txn <= 3; txn++ {
		if err := l.Append(Record{Txn: txn}, Record{Txn: txn}); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := l.Replay(4)
	if len(recs) != 2 || recs[0].Seq != 5 {
		t.Fatalf("Replay(4) = %+v", recs)
	}
	if err := l.Truncate(4); err != nil {
		t.Fatal(err)
	}
	recs, _ = l.Replay(0)
	if len(recs) != 2 || recs[0].Seq != 5 || recs[1].Seq != 6 {
		t.Fatalf("after Truncate(4): %+v", recs)
	}
	if l.LastSeq() != 6 {
		t.Fatalf("Truncate must not rewind LastSeq, got %d", l.LastSeq())
	}
}

func TestConcurrentAppendNoGapsNoDupes(t *testing.T) {
	l := New(&memSink{})
	const goroutines = 32
	const perG = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(txn uint64) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := l.Append(Record{Txn: txn}, Record{Txn: txn}); err != nil {
					t.Error(err)
				}
			}
		}(uint64(g + 1))
	}
	wg.Wait()
	recs, _ := l.Replay(0)
	want := goroutines * perG * 2
	if len(recs) != want {
		t.Fatalf("want %d records, got %d", want, len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("gap or reorder at index %d: Seq %d", i, r.Seq)
		}
		if i%2 == 1 && recs[i].Txn != recs[i-1].Txn {
			t.Fatalf("batch split at index %d", i)
		}
	}
}
