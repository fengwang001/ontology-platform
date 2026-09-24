package sink_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/record"
	"ontology/sink"
)

func buildFile(t *testing.T, n int) ([]byte, []*record.Record) {
	t.Helper()
	var buf bytes.Buffer
	w, err := sink.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var recs []*record.Record
	for i := 0; i < n; i++ {
		r, _ := record.New(record.Level(i%5), "t", record.Fields{"i": int64(i)})
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), recs
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.bin")
	_, recs := buildFile(t, 3)
	if err := sink.WriteFile(path, recs); err != nil {
		t.Fatal(err)
	}
	got, err := sink.ReadFile(path)
	if err != nil || len(got) != len(recs) {
		t.Fatalf("round trip: %v, got %d records", err, len(got))
	}
	for i := range recs {
		a, _ := recs[i].Encode()
		b, _ := got[i].Encode()
		if !bytes.Equal(a, b) {
			t.Fatalf("record %d changed", i)
		}
	}
}

func TestEveryTruncationPoint(t *testing.T) {
	data, recs := buildFile(t, 4)
	full, err := sink.ReadAll(data)
	if err != nil || len(full) != len(recs) {
		t.Fatalf("full file: %v %d records", err, len(full))
	}

	// For every cut 1..len-1: error must be one of the four classes, and the
	// recovered record count must equal the max recoverable prefix. Prefix
	// record count at offset pos is computed by rescanning the full layout.
	frameEnds := frameEndOffsets(t, data)

	classes := map[error]bool{}
	for cut := 1; cut < len(data); cut++ {
		got, err := sink.ReadAll(data[:cut])
		if err == nil {
			t.Fatalf("cut %d: expected corruption error", cut)
		}
		matched := errors.Is(err, sink.ErrHeader) ||
			errors.Is(err, sink.ErrLengthPrefix) ||
			errors.Is(err, sink.ErrBodyTruncated) ||
			errors.Is(err, sink.ErrCRC)
		if !matched {
			t.Fatalf("cut %d: unclassified error %v", cut, err)
		}
		switch {
		case errors.Is(err, sink.ErrHeader):
			classes[sink.ErrHeader] = true
		case errors.Is(err, sink.ErrLengthPrefix):
			classes[sink.ErrLengthPrefix] = true
		case errors.Is(err, sink.ErrBodyTruncated):
			classes[sink.ErrBodyTruncated] = true
		case errors.Is(err, sink.ErrCRC):
			classes[sink.ErrCRC] = true
		}
		// Recovered count = number of complete frames ending before cut.
		want := 0
		for _, end := range frameEnds {
			if end <= cut {
				want++
			}
		}
		if len(got) != want {
			t.Fatalf("cut %d: recovered %d records, prefix allows %d", cut, len(got), want)
		}
	}
	for _, e := range []error{sink.ErrHeader, sink.ErrLengthPrefix, sink.ErrBodyTruncated} {
		if !classes[e] {
			t.Fatalf("truncation class never observed: %v", e)
		}
	}
}

func TestCorruptByte(t *testing.T) {
	data, _ := buildFile(t, 2)
	body := data[8:]
	// Flip a byte inside the first record body.
	bad := bytes.Clone(data)
	bad[8+4] ^= 0xFF
	got, err := sink.ReadAll(bad)
	if !errors.Is(err, sink.ErrCRC) {
		t.Fatalf("flipped byte: got %v want ErrCRC", err)
	}
	if len(got) != 0 {
		t.Fatalf("flipped first frame: recovered %d, want 0", len(got))
	}
	// Header corruption.
	hdrBad := bytes.Clone(data)
	hdrBad[0] = 'X'
	if _, err := sink.ReadAll(hdrBad); !errors.Is(err, sink.ErrHeader) {
		t.Fatalf("bad header: got %v", err)
	}
	_ = body
}

// frameEndOffsets returns the end offset (exclusive) of every complete frame
// by walking length prefixes on the intact file.
func frameEndOffsets(t *testing.T, data []byte) []int {
	t.Helper()
	var ends []int
	pos := 8
	for pos+4 <= len(data) {
		n := int(uint32(data[pos])<<24 | uint32(data[pos+1])<<16 |
			uint32(data[pos+2])<<8 | uint32(data[pos+3]))
		if n == 0xFFFFFFFF-0 && data[pos] == 0xFF && data[pos+1] == 0xFF {
			break
		}
		end := pos + 4 + n + 4
		if end > len(data) {
			break
		}
		ends = append(ends, end)
		pos = end
	}
	return ends
}

func TestEmptyFileAndDepthField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.bin")
	if err := sink.WriteFile(path, nil); err != nil {
		t.Fatal(err)
	}
	got, err := sink.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty file recovered %d records", len(got))
	}
	// Truncating to zero/partial header bytes is a header error.
	for _, cut := range []int{1, 4, 7} {
		data, _ := os.ReadFile(path)
		if _, err := sink.ReadAll(data[:cut]); !errors.Is(err, sink.ErrHeader) {
			t.Fatalf("cut %d: got %v want header", cut, err)
		}
	}
}
