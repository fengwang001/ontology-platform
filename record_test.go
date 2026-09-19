package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func TestRecordChecksumErrorLocatesIndexAndOffset(t *testing.T) {
	data := goodFile(t, sampleRecords())
	tampered := bytes.Clone(data)
	tampered[headerSize+lenPrefixLen] ^= 0x01 // first record body byte

	_, err := Read(bytes.NewReader(tampered))
	var re *RecordError
	if !errors.As(err, &re) || !errors.Is(err, ErrRecordChecksum) {
		t.Fatalf("want record checksum error, got %v", err)
	}
	if re.Index != 0 || re.Offset != headerSize {
		t.Fatalf("location = (%d,%d), want (0,%d)", re.Index, re.Offset, headerSize)
	}
}

func TestLengthTooLargeError(t *testing.T) {
	data := goodFile(t, sampleRecords())
	tampered := bytes.Clone(data)
	putUint32(tampered[headerSize:headerSize+lenPrefixLen], 0xFFFFFFFF)

	_, err := Read(bytes.NewReader(tampered))
	var re *RecordError
	if !errors.As(err, &re) || !errors.Is(err, ErrLengthTooLarge) {
		t.Fatalf("want ErrLengthTooLarge, got %v", err)
	}
	if re.Index != 0 || re.Offset != headerSize {
		t.Fatalf("location = (%d,%d), want (0,%d)", re.Index, re.Offset, headerSize)
	}
}

func TestRecordParseError(t *testing.T) {
	data := goodFile(t, sampleRecords())
	firstLen := int(uint32At(data[headerSize : headerSize+lenPrefixLen]))
	secondOff := headerSize + recFrameOver + firstLen
	tampered := bytes.Clone(data)
	// Rewrite the second record's body with an oversized key length and patch
	// its per-record CRC so the failure is a parse error, not a checksum error.
	secondLen := int(uint32At(data[secondOff : secondOff+lenPrefixLen]))
	putUint32(tampered[secondOff+lenPrefixLen:], 0xFFFFFFFF) // huge key
	newCRC := crc32Region(tampered[secondOff+lenPrefixLen : secondOff+lenPrefixLen+secondLen])
	putUint32(tampered[secondOff+lenPrefixLen+secondLen:], newCRC)

	_, err := Read(bytes.NewReader(tampered))
	var re *RecordError
	if !errors.As(err, &re) || !errors.Is(err, ErrRecordParse) {
		t.Fatalf("want ErrRecordParse, got %v", err)
	}
	if re.Index != 1 || re.Offset != int64(secondOff) {
		t.Fatalf("location = (%d,%d), want (1,%d)", re.Index, re.Offset, secondOff)
	}
}

func TestMidRecordTruncation(t *testing.T) {
	data := goodFile(t, sampleRecords())
	// First record frame: length-prefix + body + crc. Cut through its body.
	firstLen := int(uint32At(data[headerSize : headerSize+lenPrefixLen]))
	cut := headerSize + lenPrefixLen + firstLen/2

	_, err := Read(bytes.NewReader(data[:cut]))
	var re *RecordError
	if !errors.As(err, &re) || !errors.Is(err, ErrRecordTruncated) {
		t.Fatalf("want ErrRecordTruncated, got %v", err)
	}
	if re.Index != 0 || re.Offset != headerSize {
		t.Fatalf("location = (%d,%d), want (0,%d)", re.Index, re.Offset, headerSize)
	}
}

func TestErrorOnSecondRecordCarriesIndexOne(t *testing.T) {
	data := goodFile(t, sampleRecords())
	firstLen := int(uint32At(data[headerSize : headerSize+lenPrefixLen]))
	secondOff := headerSize + recFrameOver + firstLen
	tampered := bytes.Clone(data)
	tampered[secondOff+lenPrefixLen] ^= 0xFF

	_, err := Read(bytes.NewReader(tampered))
	var re *RecordError
	if !errors.As(err, &re) {
		t.Fatalf("want RecordError, got %v", err)
	}
	if re.Index != 1 || re.Offset != int64(secondOff) {
		t.Fatalf("location = (%d,%d), want (1,%d)", re.Index, re.Offset, secondOff)
	}
}
