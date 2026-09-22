package wal

import (
	"testing"

	"ontology/codec"
	"ontology/segment"
)

func openSynced(payloads ...[]byte) (*Log, *segment.Device, []int64) {
	dev := segment.NewDevice()
	l := Open(dev)
	offs := make([]int64, 0, len(payloads))
	for _, p := range payloads {
		offs = append(offs, l.Append(p))
	}
	l.Sync()
	return l, dev, offs
}

func payloadsOf(rec Recovery) []string {
	out := make([]string, 0, len(rec.Entries))
	for _, e := range rec.Entries {
		out = append(out, string(e.Payload))
	}
	return out
}

func TestRecoverEmptyDevice(t *testing.T) {
	l, _, _ := openSynced()
	rec := l.Recover()
	if rec.Report.Reason != segment.StopEOF || rec.Report.StopAt != 0 {
		t.Fatalf("report = %+v, want EOF at 0", rec.Report)
	}
	if len(rec.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(rec.Entries))
	}
}

func TestRecoverTruncateAtRecordLastByte(t *testing.T) {
	l, dev, _ := openSynced([]byte("one"), []byte("two"), []byte("three"))
	full := dev.Len()
	// 恰好截在第三条记录的最后一个字节上：三条都必须完整回放。
	dev.Truncate(full)
	if rec := l.Recover(); len(rec.Entries) != 3 {
		t.Fatalf("no-op truncate: entries = %d, want 3", len(rec.Entries))
	}
	// 再少一个字节：第三条变成半条，必须恰好留下前两条。
	dev.Truncate(full - 1)
	rec := l.Recover()
	if rec.Report.Reason != segment.StopTruncated {
		t.Fatalf("reason = %v, want StopTruncated", rec.Report.Reason)
	}
	got := payloadsOf(rec)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("replayed %q, want [one two]", got)
	}
}

func TestRecoverTruncateMidRecord(t *testing.T) {
	l, dev, offs := openSynced([]byte("aa"), []byte("bb"), []byte("cc"))
	dev.Truncate(offs[2] + 2) // 截在第三条记录中间
	rec := l.Recover()
	if rec.Report.Reason != segment.StopTruncated || rec.Report.StopAt != offs[2] {
		t.Fatalf("report = %+v, want Truncated at %d", rec.Report, offs[2])
	}
	if len(rec.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(rec.Entries))
	}
}

func TestRecoverStopsAtFlipAndIgnoresLaterValid(t *testing.T) {
	l, dev, offs := openSynced([]byte("good-1"), []byte("bad-2"), []byte("good-3"))
	dev.Flip(offs[1]+codec.PrefixLen, 0x01) // 翻转第二条负载的一个字节
	rec := l.Recover()
	if rec.Report.Reason != segment.StopCorrupt {
		t.Fatalf("reason = %v, want StopCorrupt", rec.Report.Reason)
	}
	if rec.Report.StopAt != offs[1] {
		t.Fatalf("stop at %d, want %d", rec.Report.StopAt, offs[1])
	}
	got := payloadsOf(rec)
	if len(got) != 1 || got[0] != "good-1" {
		t.Fatalf("replayed %q, want only [good-1]", got)
	}
}

func TestRecoverZeroLengthPayload(t *testing.T) {
	l, _, _ := openSynced([]byte("x"), []byte{}, []byte("y"))
	rec := l.Recover()
	if len(rec.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(rec.Entries))
	}
	e := rec.Entries[1]
	if e.Payload == nil || len(e.Payload) != 0 {
		t.Fatalf("entry 1 payload = %v, want empty non-nil", e.Payload)
	}
}
