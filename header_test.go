package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func goodFile(t *testing.T, recs []Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, recs); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBadMagic(t *testing.T) {
	data := goodFile(t, sampleRecords())
	data[0] = 'X'
	_, err := Read(bytes.NewReader(data))
	if !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestVersionTooNewAndTooOldAreDistinct(t *testing.T) {
	data := goodFile(t, sampleRecords())

	newer := bytes.Clone(data)
	putUint16(newer[magicLen:], CurrentVersion+10)
	_, errNew := Read(bytes.NewReader(newer))
	var veNew *VersionError
	if !errors.As(errNew, &veNew) || !errors.Is(errNew, ErrVersionTooNew) {
		t.Fatalf("want ErrVersionTooNew, got %v", errNew)
	}
	if errors.Is(errNew, ErrBadMagic) || errors.Is(errNew, ErrVersionTooOld) {
		t.Fatal("too-new version misclassified")
	}

	older := bytes.Clone(data)
	putUint16(older[magicLen:], MinCompatibleVersion-1)
	_, errOld := Read(bytes.NewReader(older))
	var veOld *VersionError
	if !errors.As(errOld, &veOld) || !errors.Is(errOld, ErrVersionTooOld) {
		t.Fatalf("want ErrVersionTooOld, got %v", errOld)
	}
	if errors.Is(errOld, ErrVersionTooNew) {
		t.Fatal("too-old version misclassified as too-new")
	}
}

func TestHeaderTruncated(t *testing.T) {
	data := goodFile(t, sampleRecords())
	_, err := Read(bytes.NewReader(data[:headerSize-2]))
	if !errors.Is(err, ErrHeaderTruncated) {
		t.Fatalf("want ErrHeaderTruncated, got %v", err)
	}
}

func TestRegionChecksumDistinctFromRecordChecksum(t *testing.T) {
	data := goodFile(t, sampleRecords())

	regionBad := bytes.Clone(data)
	// Corrupt only the stored region CRC in the header; records stay valid.
	regionBad[headerSize-1] ^= 0xFF
	_, errRegion := Read(bytes.NewReader(regionBad))
	if !errors.Is(errRegion, ErrRegionChecksum) {
		t.Fatalf("want ErrRegionChecksum, got %v", errRegion)
	}
	if errors.Is(errRegion, ErrRecordChecksum) {
		t.Fatal("region checksum confused with record checksum")
	}

	recBad := bytes.Clone(data)
	// Flip a byte inside the first record body.
	recBad[headerSize+lenPrefixLen] ^= 0x01
	_, errRec := Read(bytes.NewReader(recBad))
	if !errors.Is(errRec, ErrRecordChecksum) {
		t.Fatalf("want ErrRecordChecksum, got %v", errRec)
	}
	if errors.Is(errRec, ErrRegionChecksum) {
		t.Fatal("record checksum confused with region checksum")
	}
}
