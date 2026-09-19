package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func TestCountClaimedMore(t *testing.T) {
	data := goodFile(t, sampleRecords())
	tampered := bytes.Clone(data)
	putUint32(tampered[magicLen+versionLen:magicLen+versionLen+countLen], uint32(len(sampleRecords())+2))

	_, err := Read(bytes.NewReader(tampered))
	var cm *CountMismatchError
	if !errors.As(err, &cm) {
		t.Fatalf("want CountMismatchError, got %v", err)
	}
	if !cm.ClaimedMore() {
		t.Fatalf("expected claimed-more, got claimed=%d actual=%d", cm.Claimed, cm.Actual)
	}
	if cm.Claimed != 5 || cm.Actual != 3 {
		t.Fatalf("counts = claimed %d actual %d, want 5/3", cm.Claimed, cm.Actual)
	}
	if errors.Is(err, ErrRecordChecksum) {
		t.Fatal("count mismatch confused with record corruption")
	}
}

func TestCountClaimedFewer(t *testing.T) {
	data := goodFile(t, sampleRecords())
	tampered := bytes.Clone(data)
	putUint32(tampered[magicLen+versionLen:magicLen+versionLen+countLen], uint32(len(sampleRecords())-1))

	_, err := Read(bytes.NewReader(tampered))
	var cm *CountMismatchError
	if !errors.As(err, &cm) {
		t.Fatalf("want CountMismatchError, got %v", err)
	}
	if cm.ClaimedMore() {
		t.Fatalf("expected claimed-fewer, got claimed=%d actual=%d", cm.Claimed, cm.Actual)
	}
	if cm.Claimed != 2 || cm.Actual != 3 {
		t.Fatalf("counts = claimed %d actual %d, want 2/3", cm.Claimed, cm.Actual)
	}
}
