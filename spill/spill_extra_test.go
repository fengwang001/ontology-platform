package spill

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ontology/record"
)

func TestCleanRoundTripAndZeroRecord(t *testing.T) {
	dir := t.TempDir()
	rs := mkRecords(3)
	writeRun(t, dir, 1, rs, -1)
	rd, err := Open(RunPath(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	if rd.h.Count != 3 || rd.h.RunID != 1 {
		t.Fatalf("bad header %+v", rd.h)
	}
	i := 0
	for {
		got, err := rd.ReadRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if got.Key != rs[i].Key || got.Seq != rs[i].Seq {
			t.Fatalf("rec %d mismatch", i)
		}
		i++
	}
	if i != 3 {
		t.Fatalf("read %d records", i)
	}
	rd.Close()

	writeRun(t, dir, 2, nil, -1) // zero-record run: header only
	fi, _ := os.Stat(RunPath(dir, 2))
	if fi.Size() != HeaderSize {
		t.Fatalf("zero-record run size = %d", fi.Size())
	}
	rc := Recover(RunPath(dir, 2))
	if !rc.Clean() || len(rc.Records) != 0 {
		t.Fatalf("zero-record run: %+v", rc)
	}

	empty := filepath.Join(dir, "empty.dat")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rc = Recover(empty)
	if !errors.Is(rc.HeaderErr, ErrEmptyFile) {
		t.Fatalf("empty file class = %v", rc.HeaderErr)
	}
}

func TestCRCMismatchClasses(t *testing.T) {
	dir := t.TempDir()
	rs := mkRecords(50)
	layout := frameLayout(rs)
	total := writeRun(t, dir, 1, rs, -1)

	// Flip one byte inside record 10's body: whole frame stays present,
	// CRC must mismatch; exactly 10 records form the prefix.
	path := RunPath(dir, 1)
	data, _ := os.ReadFile(path)
	pos10 := layout[10][0] + 4 + 2
	data[pos10] ^= 0xFF
	os.WriteFile(path, data, 0o600)
	rc := Recover(path)
	if !errors.Is(rc.TailErr, ErrCRC) || len(rc.Records) != 10 {
		t.Fatalf("body corruption: class=%v prefix=%d", rc.TailErr, len(rc.Records))
	}

	// Flip a header byte: header CRC mismatch, nothing recoverable.
	writeRun(t, dir, 2, rs, -1)
	path2 := RunPath(dir, 2)
	data2, _ := os.ReadFile(path2)
	data2[10] ^= 0xFF
	os.WriteFile(path2, data2, 0o600)
	rc = Recover(path2)
	if !errors.Is(rc.HeaderErr, ErrHeaderCRC) || rc.Records != nil {
		t.Fatalf("header corruption: %+v", rc)
	}

	// Trailing garbage after the announced count.
	writeRun(t, dir, 3, rs[:10], -1)
	path3 := RunPath(dir, 3)
	f, _ := os.OpenFile(path3, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write([]byte{0, 0, 0})
	f.Close()
	rc = Recover(path3)
	if !errors.Is(rc.TailErr, ErrTrailingData) || len(rc.Records) != 10 {
		t.Fatalf("trailing: class=%v n=%d", rc.TailErr, len(rc.Records))
	}
	if total <= HeaderSize {
		t.Fatal("bad fixture size")
	}
}

func TestEmptyKeyAndValue(t *testing.T) {
	dir := t.TempDir()
	rs := []record.Record{
		{Key: "", Value: nil, Seq: 0},
		{Key: "k", Value: []byte{}, Seq: 1},
	}
	writeRun(t, dir, 1, rs, -1)
	rc := Recover(RunPath(dir, 1))
	if !rc.Clean() || len(rc.Records) != 2 {
		t.Fatalf("recover: %+v", rc)
	}
	if rc.Records[0].Key != "" || len(rc.Records[0].Value) != 0 {
		t.Fatal("empty key/value not preserved")
	}
	if len(rc.Records[1].Value) != 0 || rc.Records[1].Value == nil {
		t.Fatal("empty non-nil value not preserved")
	}
}
