package repair

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/segment"
)

const (
	testTotal   = 500
	testEvery   = 4
	testPayload = 15
	testRec     = 4 + 8 + testPayload + 4 // 31
)

func buildSeg(t *testing.T, dir string) string {
	t.Helper()
	w, err := segment.NewWriter(dir, testTotal, testEvery)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < testTotal; i++ {
		if _, err := w.Append(bytes.Repeat([]byte{byte(i)}, testPayload)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, segment.SegmentName(0))
}

func wantClass(p int) error {
	if p < segment.HeaderSize {
		return segment.ErrHeaderIncomplete
	}
	switch r := (p - segment.HeaderSize) % testRec; {
	case r <= 3:
		return segment.ErrLengthPrefixIncomplete
	case r <= 3+23:
		return segment.ErrBodyIncomplete
	default:
		return segment.ErrCRCMismatch
	}
}

func TestTruncateEveryByte(t *testing.T) {
	dir := t.TempDir()
	src := buildSeg(t, dir)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != segment.HeaderSize+testTotal*testRec {
		t.Fatalf("file size %d", len(data))
	}
	counts := map[error]int{}
	trunc := filepath.Join(dir, "trunc.osl")
	for p := 1; p < len(data); p++ {
		if err := os.WriteFile(trunc, data[:p], 0o644); err != nil {
			t.Fatal(err)
		}
		err := Classify(trunc)
		if want := wantClass(p); !errors.Is(err, want) {
			t.Fatalf("p=%d: got %v, want %v", p, err, want)
		}
		counts[wantClass(p)]++
	}
	for _, e := range []error{segment.ErrHeaderIncomplete, segment.ErrLengthPrefixIncomplete,
		segment.ErrBodyIncomplete, segment.ErrCRCMismatch} {
		if counts[e] == 0 {
			t.Fatalf("class %v never produced", e)
		}
	}
	t.Logf("class counts: header=%d len=%d body=%d crc=%d",
		counts[segment.ErrHeaderIncomplete], counts[segment.ErrLengthPrefixIncomplete],
		counts[segment.ErrBodyIncomplete], counts[segment.ErrCRCMismatch])
}

func TestRepairTruncatedSegment(t *testing.T) {
	dir := t.TempDir()
	src := buildSeg(t, dir)
	data, _ := os.ReadFile(src)
	good := uint64(300)
	p := segment.HeaderSize + int(good)*testRec + 10 // 切在第 300 条的事件体中
	if err := os.WriteFile(src, data[:p], 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := RepairSegment(src, testEvery)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OldCount != testTotal || rep.NewCount != good {
		t.Fatalf("report %+v", rep)
	}
	if rep.OldCount-rep.NewCount != testTotal-good {
		t.Fatalf("delta = %d", rep.OldCount-rep.NewCount)
	}
	if err := Classify(src); err != nil {
		t.Fatalf("after repair: %v", err)
	}
	evs, err := segment.ReadAll(src)
	if err != nil || len(evs) != int(good) {
		t.Fatalf("read after repair: %d, %v", len(evs), err)
	}
}

func TestRebuildIndexByteIdentical(t *testing.T) {
	dir := t.TempDir()
	src := buildSeg(t, dir)
	idxPath := segment.IndexPath(src)
	before, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
	if err := RebuildIndex(src, testEvery); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rebuilt index differs from original")
	}
}

func TestSeqGapDetected(t *testing.T) {
	dir := t.TempDir()
	w, _ := segment.NewWriter(dir, 4, 2)
	for i := 0; i < 12; i++ {
		w.Append([]byte{byte(i)})
	}
	w.Close()
	if err := CheckContinuity(dir); err != nil {
		t.Fatalf("healthy log: %v", err)
	}
	seg1 := filepath.Join(dir, segment.SegmentName(1))
	f, _ := os.OpenFile(seg1, os.O_RDWR, 0)
	f.WriteAt(segment.EncodeHeader(segment.Header{FirstSeq: 5, Count: 4}), 0) // 缺序号 4
	f.Close()
	err := CheckContinuity(dir)
	var gap *GapError
	if !errors.As(err, &gap) || !errors.Is(err, ErrSeqGap) {
		t.Fatalf("want GapError, got %v", err)
	}
	if gap.MissingFrom != 4 || gap.MissingTo != 4 {
		t.Fatalf("gap = [%d,%d], want [4,4]", gap.MissingFrom, gap.MissingTo)
	}
}
