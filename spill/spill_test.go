package spill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/record"
)

func fixedRecords(n int) []record.Record {
	recs := make([]record.Record, n)
	for i := range recs {
		recs[i] = record.Record{
			Key:   fmt.Sprintf("k%03d", i),
			Value: []byte{byte(i), 0, 0, 0},
			Seq:   uint64(i),
		}
	}
	return recs
}

func TestWriteReadRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		recs []record.Record
	}{
		{"zero records", nil},
		{"one record", fixedRecords(1)},
		{"many records", fixedRecords(37)},
		{"empty key and value", []record.Record{{Key: "", Value: nil, Seq: 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "r.run")
			if err := WriteRun(path, tc.recs); err != nil {
				t.Fatalf("WriteRun: %v", err)
			}
			got, err := ReadAll(path)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if len(got) != len(tc.recs) {
				t.Fatalf("got %d records, want %d", len(got), len(tc.recs))
			}
			for i := range got {
				if got[i].Key != tc.recs[i].Key || got[i].Seq != tc.recs[i].Seq ||
					string(got[i].Value) != string(tc.recs[i].Value) {
					t.Fatalf("record %d mismatch: got %+v want %+v", i, got[i], tc.recs[i])
				}
			}
		})
	}
}

func TestEmptyVsZeroRecord(t *testing.T) {
	dir := t.TempDir()
	zeroRec := filepath.Join(dir, "zero.run")
	if err := WriteRun(zeroRec, nil); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadAll(zeroRec)
	if err != nil || len(recs) != 0 {
		t.Fatalf("zero-record run: recs=%d err=%v, want 0 records nil error", len(recs), err)
	}
	empty := filepath.Join(dir, "empty.run")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAll(empty); !errors.Is(err, ErrHeaderIncomplete) {
		t.Fatalf("0-byte file: want ErrHeaderIncomplete, got %v", err)
	}
	trunc := filepath.Join(dir, "trunc.run")
	if err := WriteRun(trunc, fixedRecords(3)); err != nil {
		t.Fatal(err)
	}
	if err := TruncateFileForTest(trunc, HeaderSize); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAll(trunc); !errors.Is(err, ErrLengthPrefixIncomplete) {
		t.Fatalf("header-only count=3: want ErrLengthPrefixIncomplete, got %v", err)
	}
}

// classify 计算截断到 off 字节时的期望分类与可恢复前缀长度。
// 布局：头 20B；每条 entry = 4B 前缀 + 24B payload + 4B CRC = 32B。
func classify(off int) (error, int) {
	const entry, payload = 32, 24
	if off < HeaderSize {
		return ErrHeaderIncomplete, 0
	}
	pos := off - HeaderSize
	idx, o := pos/entry, pos%entry
	switch {
	case o < 4:
		return ErrLengthPrefixIncomplete, idx
	case o < 4+payload:
		return ErrRecordBodyIncomplete, idx
	default:
		return ErrCRCMismatch, idx
	}
}

func TestTruncateEveryByte(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.run")
	recs := fixedRecords(50)
	if err := WriteRun(src, recs); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	total := len(data)
	seen := map[error]bool{}
	for off := 1; off < total; off++ {
		path := filepath.Join(dir, "t.run")
		if err := os.WriteFile(path, data[:off], 0o644); err != nil {
			t.Fatal(err)
		}
		wantErr, wantPrefix := classify(off)
		got, err := RecoverPrefix(path)
		if !errors.Is(err, wantErr) {
			t.Fatalf("off=%d: want %v, got %v", off, wantErr, err)
		}
		if len(got) != wantPrefix {
			t.Fatalf("off=%d: prefix=%d, want %d", off, len(got), wantPrefix)
		}
		for i, rec := range got {
			if rec.Key != recs[i].Key || rec.Seq != recs[i].Seq {
				t.Fatalf("off=%d: record %d corrupted", off, i)
			}
		}
		seen[wantErr] = true
	}
	for _, e := range []error{ErrHeaderIncomplete, ErrLengthPrefixIncomplete, ErrRecordBodyIncomplete, ErrCRCMismatch} {
		if !seen[e] {
			t.Fatalf("category %v never observed", e)
		}
	}
}

func TestRepairTruncatedRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.run")
	recs := fixedRecords(50)
	if err := WriteRun(path, recs); err != nil {
		t.Fatal(err)
	}
	// 截在第 10 条记录的记录体中间：20 + 9*32 + 10。
	if err := TruncateFileForTest(path, HeaderSize+9*32+10); err != nil {
		t.Fatal(err)
	}
	n, err := Repair(path)
	if err != nil || n != 9 {
		t.Fatalf("Repair: n=%d err=%v, want 9", n, err)
	}
	got, err := ReadAll(path)
	if err != nil || len(got) != 9 {
		t.Fatalf("after repair: len=%d err=%v, want 9 clean records", len(got), err)
	}
}

func TestIterator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.run")
	recs := fixedRecords(11)
	if err := WriteRun(path, recs); err != nil {
		t.Fatal(err)
	}
	it, err := NewIterator(path)
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	n := 0
	for {
		_, ok := it.Next()
		if !ok {
			break
		}
		n++
	}
	if n != 11 || it.Err() != nil {
		t.Fatalf("iterated %d records, err=%v; want 11, nil", n, it.Err())
	}
}
