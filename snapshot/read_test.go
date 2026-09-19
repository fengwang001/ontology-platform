package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"reflect"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	recs := sampleRecords()
	back, err := Read(bytes.NewReader(writeBytes(t, recs)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(back, recs) {
		t.Fatalf("got %+v, want %+v", back, recs)
	}
}

func TestReadBadMagic(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	data[0] ^= 0xFF
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
	if errors.Is(err, ErrVersionTooNew) || errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("bad magic misclassified as version error: %v", err)
	}
}

func TestReadVersionTooNew(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	binary.BigEndian.PutUint16(data[len(Magic):], CurrentVersion+1)
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrVersionTooNew) {
		t.Fatalf("got %v, want ErrVersionTooNew", err)
	}
	if errors.Is(err, ErrVersionTooOld) || errors.Is(err, ErrBadMagic) {
		t.Fatalf("version-too-new misclassified: %v", err)
	}
}

func TestReadVersionTooOld(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	binary.BigEndian.PutUint16(data[len(Magic):], MinVersion-1)
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("got %v, want ErrVersionTooOld", err)
	}
	if errors.Is(err, ErrVersionTooNew) || errors.Is(err, ErrBadMagic) {
		t.Fatalf("version-too-old misclassified: %v", err)
	}
}

func TestReadV1DefaultsAndUpgrade(t *testing.T) {
	recs := sampleRecords()
	back, err := Read(bytes.NewReader(buildV1File(recs)))
	if err != nil {
		t.Fatalf("Read v1: %v", err)
	}
	if len(back) != len(recs) {
		t.Fatalf("got %d records, want %d", len(back), len(recs))
	}
	for i, r := range back {
		if r.Key != recs[i].Key || r.Num != recs[i].Num {
			t.Fatalf("record %d = %+v, want key/num of %+v", i, r, recs[i])
		}
		if r.Value != "" {
			t.Fatalf("record %d Value = %q, want zero default", i, r.Value)
		}
	}
	out := writeBytes(t, back)
	if v := binary.BigEndian.Uint16(out[len(Magic):]); v != CurrentVersion {
		t.Fatalf("re-written version = %d, want %d", v, CurrentVersion)
	}
}

func TestReadLengthOverflow(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	binary.BigEndian.PutUint32(data[offsets[1]:], 0xFFFFFFFF)
	_, err := Read(bytes.NewReader(data))
	assertRecordErr(t, err, ErrLengthOverflow, 1, offsets[1])
}

func TestReadTruncatedMidRecord(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	cut := data[:offsets[2]+lenSize+3]
	_, err := Read(bytes.NewReader(cut))
	assertRecordErr(t, err, ErrTruncated, 2, offsets[2])
}

func TestReadTruncatedInLengthPrefix(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	cut := data[:offsets[2]+2]
	_, err := Read(bytes.NewReader(cut))
	assertRecordErr(t, err, ErrTruncated, 2, offsets[2])
}

func TestReadRecordChecksum(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	data[offsets[1]+lenSize+1] ^= 0xFF
	_, err := Read(bytes.NewReader(data))
	assertRecordErr(t, err, ErrRecordChecksum, 1, offsets[1])
}

func TestReadRecordParse(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	badBody := []byte{0, 100, 'x'}
	var region []byte
	region = append(region, data[headerSize:offsets[1]]...)
	region = append(region, frameRecord(badBody)...)
	region = append(region, data[offsets[2]:]...)
	h := header{version: CurrentVersion, count: 4, regionCRC: crc32.ChecksumIEEE(region)}
	badFile := append(h.marshal(), region...)
	_, err := Read(bytes.NewReader(badFile))
	assertRecordErr(t, err, ErrRecordParse, 1, offsets[1])
}

func TestReadCountTooLarge(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	binary.BigEndian.PutUint32(data[len(Magic)+2:], 6)
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("got %v, want ErrCountMismatch", err)
	}
	var ce *CountError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *CountError", err)
	}
	if !ce.Excess || ce.Claimed != 6 || ce.Actual != 4 {
		t.Fatalf("got %+v, want claimed=6 actual=4 excess", ce)
	}
}

func TestReadCountTooSmall(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	binary.BigEndian.PutUint32(data[len(Magic)+2:], 2)
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("got %v, want ErrCountMismatch", err)
	}
	var ce *CountError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *CountError", err)
	}
	if ce.Excess || ce.Claimed != 2 || ce.Actual != 4 {
		t.Fatalf("got %+v, want claimed=2 actual=4 not excess", ce)
	}
}

func TestReadRegionChecksum(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	data[len(Magic)+6] ^= 0xFF
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrRegionChecksum) {
		t.Fatalf("got %v, want ErrRegionChecksum", err)
	}
	var re *RecordError
	if errors.As(err, &re) {
		t.Fatalf("region checksum failure misclassified as record error: %v", err)
	}
}

func TestReadStrictReturnsNothing(t *testing.T) {
	data := writeBytes(t, sampleRecords())
	offsets := recordOffsets(t, data)
	data[offsets[1]+lenSize+1] ^= 0xFF
	recs, err := Read(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error on corrupted file")
	}
	if recs != nil {
		t.Fatalf("strict Read returned %d records on failure", len(recs))
	}
}
