package wal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "log.wal")
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := tempPath(t)
	w, err := Create(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	batches := []Batch{
		{SeqStart: 1, Payloads: [][]byte{[]byte("a"), nil, []byte("longer-payload")}},
		{SeqStart: 4, Payloads: [][]byte{[]byte("bb"), []byte("")}},
		{SeqStart: 6, Payloads: [][]byte{make([]byte, 300)}},
	}
	var maxSize int
	for _, b := range batches {
		n, err := w.Write(b)
		if err != nil {
			t.Fatal(err)
		}
		if n > maxSize {
			maxSize = n
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBatches(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(batches) {
		t.Fatalf("batches=%d want %d", len(got), len(batches))
	}
	for i := range batches {
		if got[i].SeqStart != batches[i].SeqStart || len(got[i].Payloads) != len(batches[i].Payloads) {
			t.Fatalf("batch %d mismatch", i)
		}
		for j, p := range batches[i].Payloads {
			if string(got[i].Payloads[j]) != string(p) {
				t.Fatalf("item %d/%d mismatch", i, j)
			}
		}
	}
	if maxSize <= 0 {
		t.Fatal("max batch bytes not recorded")
	}
}

func TestEmptyFile(t *testing.T) {
	path := tempPath(t)
	w, err := Create(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBatches(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty file: batches=%d err=%v", len(got), err)
	}
}

func TestTruncationClassification(t *testing.T) {
	path := tempPath(t)
	w, _ := Create(path, Options{})
	w.Write(Batch{SeqStart: 1, Payloads: [][]byte{[]byte("hello"), []byte("world"), make([]byte, 40), []byte("x"), []byte("y")}})
	w.Write(Batch{SeqStart: 6, Payloads: [][]byte{[]byte("second")}})
	w.Close()

	full, _ := os.ReadFile(path)
	// Record B1 starts at 8: prefix [8,20), items from 20, CRC tail 4.
	b1 := record(Batch{SeqStart: 1, Payloads: [][]byte{[]byte("hello"), []byte("world"), make([]byte, 40), []byte("x"), []byte("y")}})
	b1End := 8 + len(b1)
	cases := []struct {
		name string
		n    int
		want error
	}{
		{"header", 1, ErrHeaderPartial},
		{"header", 7, ErrHeaderPartial},
		{"empty-log", 8, nil},
		{"batch-header", 19, ErrBatchHeaderPartial},
		{"batch-header", 24, ErrBatchHeaderPartial},
		{"item", 30, ErrItemPartial},
		{"item", 70, ErrItemPartial},
		{"item", b1End - 2, ErrItemPartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, full[:tc.n], 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Classify(path); !errors.Is(err, tc.want) {
				t.Fatalf("n=%d got %v want %v", tc.n, err, tc.want)
			}
			got, err := ReadBatches(path)
			wantRead := tc.want
			if tc.n == 8 {
				wantRead = nil // an empty intact log is legal for ReadBatches
			}
			if !errors.Is(err, wantRead) {
				t.Fatalf("read n=%d err=%v want %v", tc.n, err, tc.want)
			}
			// Nothing visible before the first batch completes.
			if tc.n < b1End && len(got) != 0 {
				t.Fatalf("partial first batch visible: %d", len(got))
			}
		})
	}
}

func TestByteByByteTruncation(t *testing.T) {
	path := tempPath(t)
	w, _ := Create(path, Options{})
	w.Write(Batch{SeqStart: 1, Payloads: [][]byte{[]byte("abc")}})
	w.Write(Batch{SeqStart: 2, Payloads: [][]byte{[]byte("defgh")}})
	w.Close()
	full, _ := os.ReadFile(path)
	valid := map[error]bool{ErrHeaderPartial: true, ErrBatchHeaderPartial: true, ErrItemPartial: true}
	for n := 1; n < len(full); n++ {
		os.WriteFile(path, full[:n], 0o600)
		err := Classify(path)
		if err != nil && !valid[err] {
			t.Fatalf("truncate %d unexpected err %v", n, err)
		}
		got, rerr := ReadBatches(path)
		if rerr != nil && !valid[rerr] {
			t.Fatalf("truncate %d read err %v", n, rerr)
		}
		var lastSeq uint64
		for _, b := range got {
			if b.SeqStart != lastSeq+1 {
				t.Fatalf("truncate %d seq gap", n)
			}
			lastSeq = b.SeqEnd()
		}
	}
}

func TestCRCCorruptionDropsWholeBatch(t *testing.T) {
	path := tempPath(t)
	w, _ := Create(path, Options{})
	w.Write(Batch{SeqStart: 1, Payloads: [][]byte{[]byte("a"), []byte("b"), []byte("corrupt-me"), []byte("d"), []byte("e")}})
	w.Write(Batch{SeqStart: 6, Payloads: [][]byte{[]byte("next")}})
	w.Close()
	full, _ := os.ReadFile(path)
	// Flip a byte inside the third item (offset 8 + 20 + 4+1 +4+1 + 2).
	pos := 8 + 20 + (4 + 1) + (4 + 1) + 4 + 2
	full[pos] ^= 0xFF
	os.WriteFile(path, full, 0o600)
	if err := Classify(path); !errors.Is(err, ErrBatchCRC) {
		t.Fatalf("classify=%v", err)
	}
	got, err := ReadBatches(path)
	if !errors.Is(err, ErrBatchCRC) || len(got) != 0 {
		t.Fatalf("corrupt first batch must be wholly invisible: %d %v", len(got), err)
	}

	// Rebuild a clean file and corrupt the second batch: first survives.
	os.Remove(path)
	goodB1 := Batch{SeqStart: 1, Payloads: [][]byte{[]byte("a"), []byte("b"), []byte("ok"), []byte("d"), []byte("e")}}
	w2, _ := Create(path, Options{})
	w2.Write(goodB1)
	w2.Write(Batch{SeqStart: 6, Payloads: [][]byte{[]byte("next")}})
	w2.Close()
	full2, _ := os.ReadFile(path)
	b2 := record(Batch{SeqStart: 6, Payloads: [][]byte{[]byte("next")}})
	b2Start := 8 + len(record(goodB1))
	full2[b2Start+24] ^= 0xFF // inside the item payload of batch 2
	os.WriteFile(path, full2, 0o600)
	got, err = ReadBatches(path)
	if !errors.Is(err, ErrBatchCRC) || len(got) != 1 || got[0].SeqStart != 1 || len(got[0].Payloads) != 5 {
		t.Fatalf("want only first batch intact, got %d %v; %v", len(got), got, err)
	}
	_ = b2
}

func TestCrashAndSyncFail(t *testing.T) {
	cases := []struct {
		name string
		opt  Options
		want error
	}{
		{"sync-fail", Options{SyncFail: func(n int) error { return ErrBatchCRC }}, ErrBatchCRC},
		{"crash", Options{CrashAfter: 10}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tempPath(t)
			w, _ := Create(path, tc.opt)
			_, err := w.Write(Batch{SeqStart: 1, Payloads: [][]byte{[]byte("payload-data")}})
			w.Close()
			if tc.name == "sync-fail" && !errors.Is(err, ErrBatchCRC) {
				t.Fatalf("err=%v", err)
			}
			if tc.name == "crash" && err == nil {
				t.Fatal("crash write must report failure")
			}
			got, rerr := ReadBatches(path)
			if tc.name == "crash" && (len(got) != 0 || rerr == nil) {
				t.Fatalf("half batch visible: %d %v", len(got), rerr)
			}
		})
	}
}
