package wal

import (
	"errors"
	"testing"
)

type memSink struct {
	data []byte
	err  error
}

func (m *memSink) Write(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	m.data = append(m.data, p...)
	return len(p), nil
}

type shortSink struct{ n int }

func (s shortSink) Write(p []byte) (int, error) { return s.n, nil }

func TestAppendAssignsSequentialSeq(t *testing.T) {
	l := New(&memSink{})
	if err := l.Append(Record{Txn: 1, Shard: 0, Delta: -5}, Record{Txn: 1, Shard: 1, Delta: 5}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{Txn: 2, Shard: 0, Delta: -3}); err != nil {
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
			t.Fatalf("record %d has Seq %d, want %d", i, r.Seq, i+1)
		}
	}
	if l.LastSeq() != 3 {
		t.Fatalf("LastSeq = %d, want 3", l.LastSeq())
	}
}

func TestAppendFailureIsAllOrNothing(t *testing.T) {
	sink := &memSink{}
	l := New(sink)
	if err := l.Append(Record{Txn: 1, Shard: 0, Delta: 1}); err != nil {
		t.Fatal(err)
	}
	sink.err = errors.New("disk on fire")
	err := l.Append(Record{Txn: 2, Shard: 0, Delta: 2}, Record{Txn: 2, Shard: 1, Delta: -2})
	if err == nil {
		t.Fatal("expected error from failing sink")
	}
	if l.LastSeq() != 1 {
		t.Fatalf("failed append consumed Seq: LastSeq = %d, want 1", l.LastSeq())
	}
	recs, _ := l.Replay(0)
	if len(recs) != 1 {
		t.Fatalf("failed append left records: got %d, want 1", len(recs))
	}
	// 失败后 Seq 不占用：下一条成功记录应拿到 Seq 2。
	sink.err = nil
	if err := l.Append(Record{Txn: 3, Shard: 0, Delta: 7}); err != nil {
		t.Fatal(err)
	}
	recs, _ = l.Replay(0)
	if recs[len(recs)-1].Seq != 2 {
		t.Fatalf("Seq after failed append = %d, want 2", recs[len(recs)-1].Seq)
	}
}

func TestAppendShortWriteFails(t *testing.T) {
	l := New(shortSink{n: 3})
	if err := l.Append(Record{Txn: 1, Shard: 0, Delta: 1}); err == nil {
		t.Fatal("expected short write error")
	}
	if l.LastSeq() != 0 {
		t.Fatalf("short write consumed Seq: LastSeq = %d", l.LastSeq())
	}
}

func TestAppendWritesEncodedGroupToSink(t *testing.T) {
	sink := &memSink{}
	l := New(sink)
	want := []Record{
		{Txn: 9, Shard: 2, Delta: -10},
		{Txn: 9, Shard: 3, Delta: 10},
	}
	if err := l.Append(want...); err != nil {
		t.Fatal(err)
	}
	got := decode(sink.data)
	if len(got) != 2 {
		t.Fatalf("decoded %d records, want 2", len(got))
	}
	for i, r := range got {
		if r.Txn != want[i].Txn || r.Shard != want[i].Shard || r.Delta != want[i].Delta {
			t.Fatalf("record %d = %+v, want %+v", i, r, want[i])
		}
		if r.Seq != uint64(i+1) {
			t.Fatalf("record %d Seq = %d, want %d", i, r.Seq, i+1)
		}
	}
}

func TestReplayFromAndTruncate(t *testing.T) {
	l := New(&memSink{})
	for i := 1; i <= 5; i++ {
		if err := l.Append(Record{Txn: uint64(i), Shard: 0, Delta: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := l.Replay(3)
	if len(recs) != 2 || recs[0].Seq != 4 || recs[1].Seq != 5 {
		t.Fatalf("Replay(3) = %+v", recs)
	}
	if err := l.Truncate(3); err != nil {
		t.Fatal(err)
	}
	recs, _ = l.Replay(0)
	if len(recs) != 2 || recs[0].Seq != 4 {
		t.Fatalf("after Truncate(3), Replay(0) = %+v", recs)
	}
	// 截断不影响 Seq 单调递增。
	if err := l.Append(Record{Txn: 6, Shard: 0, Delta: 6}); err != nil {
		t.Fatal(err)
	}
	if l.LastSeq() != 6 {
		t.Fatalf("LastSeq after truncate+append = %d, want 6", l.LastSeq())
	}
}
