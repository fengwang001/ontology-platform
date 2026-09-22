package segment

import (
	"bytes"
	"testing"

	"ontology/codec"
)

func collect(dev *Device, limit int64) ([][]byte, ScanReport) {
	var got [][]byte
	rep := New(dev).Scan(0, limit, func(_ int64, p []byte) bool {
		got = append(got, p)
		return true
	})
	return got, rep
}

func TestAppendThenScanAll(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	want := [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")}
	for _, p := range want {
		seg.Append(p)
	}
	got, rep := collect(dev, dev.Len())
	if rep.Reason != StopEOF {
		t.Fatalf("reason = %v, want StopEOF", rep.Reason)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("record %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScanStopsAtFirstCorruption(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	seg.Append([]byte("good-1"))
	badOff := seg.Append([]byte("bad-2"))
	seg.Append([]byte("good-3")) // 损坏点之后的完整记录，不许捡回来
	dev.Flip(badOff+codec.PrefixLen, 0xFF)

	got, rep := collect(dev, dev.Len())
	if rep.Reason != StopCorrupt {
		t.Fatalf("reason = %v, want StopCorrupt", rep.Reason)
	}
	if rep.StopAt != badOff {
		t.Fatalf("stop at %d, want %d", rep.StopAt, badOff)
	}
	if len(got) != 1 || string(got[0]) != "good-1" {
		t.Fatalf("replayed %q, want only [good-1]", got)
	}
}

func TestScanStopsAtTruncation(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	seg.Append([]byte("full"))
	half := seg.Append([]byte("half-record"))
	dev.Truncate(half + 3) // 只留半条记录的长度前缀残片

	got, rep := collect(dev, dev.Len())
	if rep.Reason != StopTruncated {
		t.Fatalf("reason = %v, want StopTruncated", rep.Reason)
	}
	if rep.StopAt != half {
		t.Fatalf("stop at %d, want %d", rep.StopAt, half)
	}
	if rep.Detail.Kind != codec.KindTruncated || rep.Detail.Reason != codec.TruncPrefix {
		t.Fatalf("detail = %+v, want Truncated/Prefix", rep.Detail)
	}
	if len(got) != 1 {
		t.Fatalf("replayed %d records, want 1", len(got))
	}
}

func TestScanEmptyDevice(t *testing.T) {
	dev := NewDevice()
	got, rep := collect(dev, dev.Len())
	if rep.Reason != StopEOF || rep.StopAt != 0 {
		t.Fatalf("report = %+v, want EOF at 0", rep)
	}
	if len(got) != 0 {
		t.Fatalf("replayed %d records, want 0", len(got))
	}
}
