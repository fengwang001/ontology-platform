package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestInspectCleanFile(t *testing.T) {
	rep := Inspect(bytes.NewReader(writeBytes(t, sampleRecords())))
	if rep.HeaderErr != nil || rep.Reason != nil || rep.BadIndex != -1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if len(rep.Records) != 4 {
		t.Fatalf("prefix = %d, want 4", len(rep.Records))
	}
}

func TestInspectCorruptRecord(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	data[offsets[1]+lenSize+1] ^= 0xFF
	rep := Inspect(bytes.NewReader(data))
	if len(rep.Records) != 1 || rep.Records[0].Key != "alpha" {
		t.Fatalf("prefix = %+v, want [alpha]", rep.Records)
	}
	if rep.BadIndex != 1 || rep.BadOffset != int64(offsets[1]) {
		t.Fatalf("bad point = record %d offset %d, want record 1 offset %d",
			rep.BadIndex, rep.BadOffset, offsets[1])
	}
	if !errors.Is(rep.Reason, ErrRecordChecksum) {
		t.Fatalf("reason = %v, want ErrRecordChecksum", rep.Reason)
	}
	if rep.RecoverableAfter != 2 {
		t.Fatalf("recoverable after = %d, want 2", rep.RecoverableAfter)
	}
}

func TestInspectCorruptLengthResync(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	binary.BigEndian.PutUint32(data[offsets[1]:], 0x00FFFFFF)
	rep := Inspect(bytes.NewReader(data))
	if !errors.Is(rep.Reason, ErrLengthOverflow) {
		t.Fatalf("reason = %v, want ErrLengthOverflow", rep.Reason)
	}
	if rep.BadIndex != 1 || len(rep.Records) != 1 {
		t.Fatalf("bad index = %d prefix = %d, want 1/1", rep.BadIndex, len(rep.Records))
	}
	if rep.RecoverableAfter != 2 {
		t.Fatalf("recoverable after = %d, want 2", rep.RecoverableAfter)
	}
}

func TestInspectTruncated(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	cut := data[:offsets[2]+lenSize+2]
	rep := Inspect(bytes.NewReader(cut))
	if len(rep.Records) != 2 {
		t.Fatalf("prefix = %d, want 2", len(rep.Records))
	}
	if rep.BadIndex != 2 || !errors.Is(rep.Reason, ErrTruncated) {
		t.Fatalf("bad index = %d reason = %v, want 2/ErrTruncated", rep.BadIndex, rep.Reason)
	}
	if rep.RecoverableAfter != 0 {
		t.Fatalf("recoverable after = %d, want 0", rep.RecoverableAfter)
	}
}

func TestInspectCorruptLastRecord(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	data[offsets[3]+lenSize] ^= 0xFF
	rep := Inspect(bytes.NewReader(data))
	if len(rep.Records) != 3 || rep.BadIndex != 3 {
		t.Fatalf("prefix = %d bad index = %d, want 3/3", len(rep.Records), rep.BadIndex)
	}
	if rep.RecoverableAfter != 0 {
		t.Fatalf("recoverable after = %d, want 0", rep.RecoverableAfter)
	}
}

func TestInspectBadHeader(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	data[0] ^= 0xFF
	rep := Inspect(bytes.NewReader(data))
	if !errors.Is(rep.HeaderErr, ErrBadMagic) {
		t.Fatalf("header err = %v, want ErrBadMagic", rep.HeaderErr)
	}
	if len(rep.Records) != 0 {
		t.Fatalf("prefix = %d, want 0", len(rep.Records))
	}
}

func TestInspectDoesNotAffectRead(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	data[offsets[1]+lenSize+1] ^= 0xFF
	rep := Inspect(bytes.NewReader(data))
	if len(rep.Records) != 1 {
		t.Fatalf("inspect prefix = %d, want 1", len(rep.Records))
	}
	recs, err := Read(bytes.NewReader(data))
	if err == nil || recs != nil {
		t.Fatalf("strict Read on same bytes = %v records, err %v", len(recs), err)
	}
}
