package snapshot

import (
	"bytes"
	"hash/crc32"
	"testing"
)

// buildRaw writes a file at an arbitrary version using the real frame format.
func buildRaw(t *testing.T, version uint16, recs []Record) []byte {
	t.Helper()
	var region bytes.Buffer
	for _, r := range recs {
		body := encodeBody(r, version)
		frame := make([]byte, recFrameOver+len(body))
		putUint32(frame, uint32(len(body)))
		copy(frame[lenPrefixLen:lenPrefixLen+len(body)], body)
		putUint32(frame[lenPrefixLen+len(body):], crc32.ChecksumIEEE(body))
		region.Write(frame)
	}
	out := make([]byte, headerSize)
	copy(out[0:magicLen], Magic)
	putUint16(out[magicLen:], version)
	putUint32(out[magicLen+versionLen:], uint32(len(recs)))
	putUint32(out[magicLen+versionLen+countLen:], crc32.ChecksumIEEE(region.Bytes()))
	return append(out, region.Bytes()...)
}

func TestV1ReadDefaultsNoteAndUpgradesOnRewrite(t *testing.T) {
	v1Recs := []Record{{Key: "k1", Value: 42}, {Key: "k2", Value: 7}}
	data := buildRaw(t, 1, v1Recs)

	got, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	for i := range got {
		if got[i].Note != "" {
			t.Fatalf("record %d note = %q, want empty default", i, got[i].Note)
		}
	}
	if got[0] != (Record{Key: "k1", Value: 42}) || got[1] != (Record{Key: "k2", Value: 7}) {
		t.Fatalf("v1 records mismatch: %+v", got)
	}

	var upgraded bytes.Buffer
	if err := Write(&upgraded, got); err != nil {
		t.Fatal(err)
	}
	if version := uint16At(upgraded.Bytes()[magicLen : magicLen+versionLen]); version != CurrentVersion {
		t.Fatalf("rewritten version = %d, want %d", version, CurrentVersion)
	}
	again, err := Read(&upgraded)
	if err != nil {
		t.Fatalf("read upgraded: %v", err)
	}
	if len(again) != 2 || again[0].Key != "k1" {
		t.Fatalf("upgraded round trip mismatch: %+v", again)
	}
}
