package logkv

import (
	"bytes"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	buf := EncodeRecord(42, []byte("k"), []byte("v"), false)
	if len(buf) != RecordSize(1, 1) {
		t.Fatalf("size=%d", len(buf))
	}
	res, err := scanSegment(1, bytes.NewReader(buf), int64(len(buf)), false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.records) != 1 || res.records[0].Seq != 42 || res.records[0].Tombstone {
		t.Fatalf("records=%+v", res.records)
	}
	if res.validBytes != int64(len(buf)) || res.tornBytes != 0 {
		t.Fatalf("valid=%d torn=%d", res.validBytes, res.tornBytes)
	}
}

// TestScanTornKinds 覆盖撕裂尾部的三种形态：头不完整、声明长度
// 越过段末、恰为最后一条且校验失败；以及它们在已封口段中都是
// 段损坏。
func TestScanTornKinds(t *testing.T) {
	good := EncodeRecord(1, []byte("a"), []byte("1"), false)
	last := EncodeRecord(2, []byte("b"), []byte("2"), false)

	cases := map[string][]byte{
		"incomplete-header": append(append([]byte{}, good...), last[:7]...),
		"length-past-end":   append(append([]byte{}, good...), last[:len(last)-3]...),
		"bad-crc-last":      append(append([]byte{}, good...), flipBit(last, 10)...),
	}
	for name, data := range cases {
		res, err := scanSegment(1, bytes.NewReader(data), int64(len(data)), true)
		if err != nil {
			t.Fatalf("%s: active scan: %v", name, err)
		}
		if res.validBytes != int64(len(good)) || res.tornBytes != int64(len(data)-len(good)) {
			t.Fatalf("%s: valid=%d torn=%d", name, res.validBytes, res.tornBytes)
		}
		if len(res.records) != 1 {
			t.Fatalf("%s: records=%d", name, len(res.records))
		}
		if _, err := scanSegment(1, bytes.NewReader(data), int64(len(data)), false); !IsKind(err, KindSegmentCorruption) {
			t.Fatalf("%s: sealed scan should be corruption, got %v", name, err)
		}
	}
}

// TestScanMidCorruption 活动段中段校验失败是段损坏而非撕裂。
func TestScanMidCorruption(t *testing.T) {
	first := EncodeRecord(1, []byte("a"), []byte("1"), false)
	last := EncodeRecord(2, []byte("b"), []byte("2"), false)
	data := append(flipBit(first, 10), last...)
	_, err := scanSegment(1, bytes.NewReader(data), int64(len(data)), true)
	if !IsKind(err, KindSegmentCorruption) {
		t.Fatalf("mid corruption: %v", err)
	}
	if e := err.(*Error); e.Offset != 0 {
		t.Fatalf("offset=%d want 0", e.Offset)
	}
}
