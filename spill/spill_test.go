package spill

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"ontology/record"
)

func makeRecords(n int) []record.Record {
	recs := make([]record.Record, n)
	for i := range recs {
		recs[i] = record.Record{
			Key:   fmt.Sprintf("key-%03d", i%7),
			Value: bytes.Repeat([]byte{byte(i)}, i%11+1),
			Seq:   uint64(i + 1),
		}
	}
	return recs
}

func TestWriteReadRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"zero records", 0},
		{"one record", 1},
		{"fifty records", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs := makeRecords(tc.n)
			var buf bytes.Buffer
			if err := WriteRun(&buf, recs); err != nil {
				t.Fatal(err)
			}
			got, err := ReadRun(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(recs) {
				t.Fatalf("got %d records want %d", len(got), len(recs))
			}
			for i := range recs {
				if got[i].Key != recs[i].Key || got[i].Seq != recs[i].Seq ||
					!bytes.Equal(got[i].Value, recs[i].Value) {
					t.Fatalf("record %d mismatch", i)
				}
			}
		})
	}
}

// TestTruncateEveryByte walks every truncation point of a 50-record run
// and asserts the recovery classification and maximal recoverable prefix.
func TestTruncateEveryByte(t *testing.T) {
	recs := makeRecords(50)
	var full bytes.Buffer
	if err := WriteRun(&full, recs); err != nil {
		t.Fatal(err)
	}
	data := full.Bytes()
	off := HeaderSize
	type span struct{ start, prefixEnd, end int }
	spans := make([]span, len(recs))
	for i, r := range recs {
		sz := 4 + r.EncodedLen() + 4
		spans[i] = span{off, off + 4, off + sz}
		off += sz
	}
	if off != len(data) {
		t.Fatalf("layout mismatch: %d != %d", off, len(data))
	}
	for cut := 1; cut < len(data); cut++ {
		wantErr := error(nil)
		wantN := len(recs)
		if cut < HeaderSize {
			wantErr, wantN = ErrHeaderIncomplete, 0
		} else {
			for i, s := range spans {
				if cut >= s.start && cut < s.end {
					wantN = i
					if cut < s.prefixEnd {
						wantErr = ErrLengthPrefixIncomplete
					} else {
						wantErr = ErrRecordBodyIncomplete
					}
					break
				}
			}
			if wantErr == nil {
				wantErr = ErrLengthPrefixIncomplete
				for i, s := range spans {
					if cut == s.start {
						wantN = i
					}
				}
			}
		}
		trunc, err := WriteRunTruncated(recs, cut)
		if err != nil {
			t.Fatal(err)
		}
		got, rerr := Recover(bytes.NewReader(trunc))
		if !errors.Is(rerr, wantErr) {
			t.Fatalf("cut=%d: err=%v want %v", cut, rerr, wantErr)
		}
		if len(got) != wantN {
			t.Fatalf("cut=%d: recovered %d want %d", cut, len(got), wantN)
		}
		for i := range got {
			if got[i].Seq != recs[i].Seq {
				t.Fatalf("cut=%d: prefix record %d corrupted", cut, i)
			}
		}
	}
}

func TestCorruptionAndEmptyRuns(t *testing.T) {
	recs := makeRecords(10)
	var full bytes.Buffer
	if err := WriteRun(&full, recs); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr error
		wantN   int
	}{
		{"crc mismatch first", func(b []byte) []byte {
			b[HeaderSize+4] ^= 0xff
			return b
		}, ErrCRCMismatch, 0},
		{"crc mismatch last", func(b []byte) []byte {
			b[len(b)-1] ^= 0xff
			return b
		}, ErrCRCMismatch, 9},
		{"empty file", func(b []byte) []byte { return nil }, ErrHeaderIncomplete, 0},
		{"bad magic", func(b []byte) []byte {
			copy(b, "XXXX")
			return b
		}, ErrBadMagic, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.mutate(append([]byte(nil), full.Bytes()...))
			got, err := Recover(bytes.NewReader(data))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if len(got) != tc.wantN {
				t.Fatalf("recovered %d want %d", len(got), tc.wantN)
			}
		})
	}
}

// TestZeroRecordRunDistinguishable: a valid zero-record run (header only,
// count=0) reads back cleanly, while a header claiming records but holding
// none is a decidable truncation error.
func TestZeroRecordRunDistinguishable(t *testing.T) {
	var empty bytes.Buffer
	if err := WriteRun(&empty, nil); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRun(bytes.NewReader(empty.Bytes()))
	if err != nil || len(got) != 0 {
		t.Fatalf("zero-record run: n=%d err=%v", len(got), err)
	}
	claimed := append([]byte(nil), empty.Bytes()...)
	claimed[6] = 3
	if _, err := Recover(bytes.NewReader(claimed)); !errors.Is(err, ErrLengthPrefixIncomplete) {
		t.Fatalf("empty run with count>0: err=%v", err)
	}
}
