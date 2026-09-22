package spill

import (
	"errors"
	"io"
	"os"
	"testing"

	"ontology/record"
)

func mkRecords(n int) []record.Record {
	rs := make([]record.Record, n)
	for i := 0; i < n; i++ {
		key := "key-" + string(rune('a'+i%7)) + "-" + itoa(i)
		val := make([]byte, (i*13)%37)
		for j := range val {
			val[j] = byte(i + j)
		}
		rs[i] = record.Record{Key: key, Value: val, Seq: uint64(i)}
	}
	return rs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// frameLayout computes each record region's [start,end) byte range.
func frameLayout(rs []record.Record) [][2]int {
	out := make([][2]int, len(rs))
	pos := HeaderSize
	for i, r := range rs {
		frame := record.AppendFrame(nil, r)
		end := pos + 4 + len(frame) + crcLen
		out[i] = [2]int{pos, end}
		pos = end
	}
	return out
}

func writeRun(t *testing.T, dir string, id uint64, rs []record.Record, cut int) int {
	t.Helper()
	w, err := Create(dir, id, rs)
	if err != nil {
		t.Fatal(err)
	}
	total := w.BytesWritten()
	if err := w.Close(cut); err != nil {
		t.Fatal(err)
	}
	return total
}

func TestEveryTruncationPoint(t *testing.T) {
	dir := t.TempDir()
	rs := mkRecords(50)
	layout := frameLayout(rs)
	total := layout[49][1]
	for cut := 1; cut < total; cut++ {
		path := RunPath(dir, 999)
		os.Remove(path)
		w, err := Create(dir, 999, rs)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.BytesWritten(); got != total {
			t.Fatalf("size mismatch %d vs %d", got, total)
		}
		if err := w.Close(cut); err != nil {
			t.Fatal(err)
		}
		rec := Recover(path)
		wantClass, wantPrefix := classify(cut, layout)
		gotClass := rec.TailErr
		if cut < HeaderSize {
			gotClass = rec.HeaderErr
		}
		if !errors.Is(gotClass, wantClass) {
			t.Fatalf("cut=%d: class=%v want %v", cut, gotClass, wantClass)
		}
		if len(rec.Records) != wantPrefix {
			t.Fatalf("cut=%d: prefix=%d want %d", cut, len(rec.Records), wantPrefix)
		}
		for i, got := range rec.Records {
			if got.Key != rs[i].Key || got.Seq != rs[i].Seq ||
				string(got.Value) != string(rs[i].Value) {
				t.Fatalf("cut=%d rec %d corrupted", cut, i)
			}
		}
	}
}

func classify(cut int, layout [][2]int) (error, int) {
	if cut < HeaderSize {
		return ErrHeaderIncomplete, 0
	}
	prefix := 0
	for _, span := range layout {
		start, end := span[0], span[1]
		switch {
		case cut >= end:
			prefix++
		case cut < start+4:
			return ErrLengthPrefixIncomplete, prefix
		default:
			return ErrRecordIncomplete, prefix
		}
	}
	return io.EOF, prefix
}
