package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func TestInspectRecoverablePrefixAndSkipped(t *testing.T) {
	recs := []Record{
		{Key: "a", Value: 1, Note: "x"},
		{Key: "b", Value: 2, Note: "y"},
		{Key: "c", Value: 3, Note: "z"},
		{Key: "d", Value: 4, Note: "w"},
	}
	data := goodFile(t, recs)

	// Damage record index 1 (one body byte).
	firstLen := int(uint32At(data[headerSize : headerSize+lenPrefixLen]))
	secondOff := headerSize + recFrameOver + firstLen
	tampered := bytes.Clone(data)
	tampered[secondOff+lenPrefixLen] ^= 0xFF

	// Read stays strict and fails.
	if _, err := Read(bytes.NewReader(tampered)); err == nil {
		t.Fatal("Read must fail on corruption")
	}

	rep := Inspect(bytes.NewReader(tampered))
	if rep.HeaderErr != nil {
		t.Fatalf("unexpected header error: %v", rep.HeaderErr)
	}
	if len(rep.Records) != 1 || rep.Records[0].Key != "a" {
		t.Fatalf("recovered prefix = %+v, want just [a]", rep.Records)
	}
	if rep.BadIndex != 1 || rep.BadOffset != int64(secondOff) {
		t.Fatalf("bad point = (%d,%d), want (1,%d)", rep.BadIndex, rep.BadOffset, secondOff)
	}
	if !errors.Is(rep.BadErr, ErrRecordChecksum) {
		t.Fatalf("bad reason = %v, want record checksum", rep.BadErr)
	}
	// Records c and d remain intact after the bad frame.
	if rep.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", rep.Skipped)
	}
}

func TestInspectCleanFile(t *testing.T) {
	data := goodFile(t, sampleRecords())
	rep := Inspect(bytes.NewReader(data))
	if rep.BadIndex != -1 || rep.CountErr != nil || rep.RegionErr {
		t.Fatalf("clean file reported problems: %+v", rep)
	}
	if len(rep.Records) != 3 {
		t.Fatalf("recovered %d records, want 3", len(rep.Records))
	}
}

func TestInspectTruncation(t *testing.T) {
	data := goodFile(t, sampleRecords())
	firstLen := int(uint32At(data[headerSize : headerSize+lenPrefixLen]))
	cut := headerSize + recFrameOver + firstLen + lenPrefixLen + 1
	rep := Inspect(bytes.NewReader(data[:cut]))
	if rep.BadIndex != 1 || !errors.Is(rep.BadErr, ErrRecordTruncated) {
		t.Fatalf("want truncated record 1, got idx=%d err=%v", rep.BadIndex, rep.BadErr)
	}
	if len(rep.Records) != 1 {
		t.Fatalf("prefix len = %d, want 1", len(rep.Records))
	}
}

func TestInspectCountMismatch(t *testing.T) {
	data := goodFile(t, sampleRecords())

	more := bytes.Clone(data)
	putUint32(more[magicLen+versionLen:magicLen+versionLen+countLen], 9)
	repMore := Inspect(bytes.NewReader(more))
	if repMore.CountErr == nil || !repMore.CountErr.ClaimedMore() {
		t.Fatalf("expected claimed-more, got %+v", repMore.CountErr)
	}
	if len(repMore.Records) != 3 {
		t.Fatalf("prefix len = %d, want 3", len(repMore.Records))
	}

	fewer := bytes.Clone(data)
	putUint32(fewer[magicLen+versionLen:magicLen+versionLen+countLen], 1)
	repFewer := Inspect(bytes.NewReader(fewer))
	if repFewer.CountErr == nil || repFewer.CountErr.ClaimedMore() {
		t.Fatalf("expected claimed-fewer, got %+v", repFewer.CountErr)
	}
}
