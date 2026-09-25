package repair

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

const (
	testEvents = 500
	payloadLen = 4
	recordLen  = 4 + event.HeaderLen + payloadLen + 4 // 20
)

func writeFullSeg(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	path := filepath.Join(dir, "seg.log")
	w, err := segment.Create(path, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < testEvents; i++ {
		p := []byte{byte(i), byte(i >> 8), 0xAB, 0xCD}
		if err := w.Append(event.Encode(event.Event{Seq: uint64(i), Payload: p})); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

func classifyTruncated(t *testing.T, dir string, data []byte, n int) error {
	t.Helper()
	p := filepath.Join(dir, "trunc.log")
	if err := os.WriteFile(p, data[:n], 0o644); err != nil {
		t.Fatal(err)
	}
	return Classify(p)
}

func TestTruncateClassifyEveryByte(t *testing.T) {
	dir := t.TempDir()
	_, data := writeFullSeg(t, dir)
	sentinels := []error{
		segment.ErrHeaderIncomplete, segment.ErrLengthPrefixIncomplete,
		segment.ErrEventBodyIncomplete, segment.ErrCRCMismatch,
	}
	counts := map[string]int{}
	for n := 1; n < len(data); n++ {
		err := classifyTruncated(t, dir, data, n)
		if err == nil {
			t.Fatalf("truncation at %d classified clean", n)
		}
		matched := false
		for _, s := range sentinels {
			if errors.Is(err, s) {
				matched = true
				counts[s.Error()]++
			}
		}
		if !matched {
			t.Fatalf("truncation at %d: unclassified error %v", n, err)
		}
		var want error
		switch {
		case n < segment.HeaderSize:
			want = segment.ErrHeaderIncomplete
		case (n-segment.HeaderSize)%recordLen < 4:
			want = segment.ErrLengthPrefixIncomplete
		default:
			want = segment.ErrEventBodyIncomplete
		}
		if !errors.Is(err, want) {
			t.Fatalf("truncation at %d: got %v, want %v", n, err, want)
		}
	}
	if counts[segment.ErrHeaderIncomplete.Error()] != segment.HeaderSize-1 {
		t.Fatalf("header-incomplete count = %d", counts[segment.ErrHeaderIncomplete.Error()])
	}
	t.Logf("classification counts: %v", counts)
}

func TestCRCMismatchOnCorruption(t *testing.T) {
	dir := t.TempDir()
	_, data := writeFullSeg(t, dir)
	cases := []struct {
		name string
		off  int
	}{
		{"flip in event body", segment.HeaderSize + 6},
		{"flip in crc field", segment.HeaderSize + recordLen - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data[tc.off] ^= 0xFF
			defer func() { data[tc.off] ^= 0xFF }()
			if err := classifyTruncated(t, dir, data, len(data)); !errors.Is(err, segment.ErrCRCMismatch) {
				t.Fatalf("err = %v, want ErrCRCMismatch", err)
			}
		})
	}
}

func TestRepairSegmentFixesCount(t *testing.T) {
	dir := t.TempDir()
	_, data := writeFullSeg(t, dir)
	cut := segment.HeaderSize + 300*recordLen + 7 // mid record 300
	p := filepath.Join(dir, "trunc.log")
	if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	before, after, err := RepairSegment(p)
	if err != nil {
		t.Fatal(err)
	}
	if before != testEvents || after != 300 || before-after != 200 {
		t.Fatalf("before=%d after=%d", before, after)
	}
	if err := Classify(p); err != nil {
		t.Fatalf("repaired segment still broken: %v", err)
	}
	_, good, _, err := segment.Scan(p, nil)
	if err != nil || good != 300 {
		t.Fatalf("rescan good=%d err=%v", good, err)
	}
}

func TestRebuildIndexByteIdentical(t *testing.T) {
	dir := t.TempDir()
	segPath, _ := writeFullSeg(t, dir)
	idx, err := sparse.Build(segPath)
	if err != nil {
		t.Fatal(err)
	}
	idxPath := segPath + ".idx"
	if err := sparse.WriteFile(idxPath, idx); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(idxPath)
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	if err := RebuildIndex(segPath); err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := os.ReadFile(idxPath)
	if string(original) != string(rebuilt) {
		t.Fatalf("rebuilt index differs from original")
	}
}

func TestCheckGaps(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, first, count uint64) string {
		p := filepath.Join(dir, name)
		w, err := segment.Create(p, 4, first)
		if err != nil {
			t.Fatal(err)
		}
		for i := uint64(0); i < count; i++ {
			if err := w.Append(event.Encode(event.Event{Seq: first + i})); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := mk("a.log", 0, 10)
	b := mk("b.log", 12, 5) // seq 10, 11 missing
	c := mk("c.log", 10, 2) // continuous with a
	gaps, err := CheckGaps([]string{a, b})
	if !errors.Is(err, ErrSeqGap) {
		t.Fatalf("err = %v, want ErrSeqGap", err)
	}
	if len(gaps) != 1 || gaps[0] != (Gap{From: 10, To: 11}) {
		t.Fatalf("gaps = %v", gaps)
	}
	if gaps, err := CheckGaps([]string{a, c}); err != nil || gaps != nil {
		t.Fatalf("continuous: gaps=%v err=%v", gaps, err)
	}
}
