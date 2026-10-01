package fragmentlog

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestTruncation(t *testing.T) {
	const B = 16
	var stream []byte
	stream, _ = naiveAppend(B, stream, []byte("hello")) // 12 bytes: 7 hdr + 5 data

	// Partial header (5 of 7 bytes).
	rd := mustReader(t, stream[:5], B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrTruncated) || off != 0 {
		t.Fatalf("partial header: want ErrTruncated at 0, got (%v, %d)", err, off)
	}
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after truncation want EOF, got %v", err)
	}
	t.Logf("头部只有 5/7 字节 -> ErrTruncated @0, 下一次 Next 返回 io.EOF")

	// Header complete, data short (9 of 12 bytes).
	rd = mustReader(t, stream[:9], B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrTruncated) || off != 0 {
		t.Fatalf("short data: want ErrTruncated at 0, got (%v, %d)", err, off)
	}
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after truncation want EOF, got %v", err)
	}
	t.Logf("数据只有 2/5 字节 -> ErrTruncated @0 (判定: 数据字节不足), 下一次为 io.EOF")

	// First fragment complete, Last never comes.
	var multi []byte
	multi, _ = naiveAppend(B, multi, bytes.Repeat([]byte{0x77}, 20)) // First@0, Last@16
	rd = mustReader(t, multi[:B], B)
	if _, off, err := rd.Next(); !errors.Is(err, ErrTruncated) || off != B {
		t.Fatalf("unfinished record: want ErrTruncated at %d, got (%v, %d)", B, err, off)
	}
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after truncation want EOF, got %v", err)
	}
	t.Logf("首片完整但末片缺失 -> ErrTruncated @%d (判定: 记录未收尾处结束)", B)
}

func TestCleanEOF(t *testing.T) {
	rd := mustReader(t, nil, 16)
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream: want EOF, got %v", err)
	}
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next: want EOF, got %v", err)
	}

	var stream []byte
	stream, _ = naiveAppend(16, stream, []byte("a"))
	stream, _ = naiveAppend(16, stream, []byte("b"))
	rd = mustReader(t, stream, 16)
	for i, want := range []string{"a", "b"} {
		rec, _, err := rd.Next()
		if err != nil || string(rec) != want {
			t.Fatalf("record %d: (%q, %v)", i, rec, err)
		}
	}
	if _, _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("clean end: want EOF, got %v", err)
	}
	t.Logf("空流与干净流尾均返回 io.EOF, 且 EOF 之后保持 io.EOF")
}

// TestErrorDiscardsAssembly verifies that an error discards the
// in-progress record including the offending fragment, and that reading
// resumes at the next block boundary.
func TestErrorDiscardsAssembly(t *testing.T) {
	const B = 16
	var stream []byte
	stream, _ = naiveAppend(B, stream, bytes.Repeat([]byte{0x11}, 27)) // First@0 Middle@16 Last@32, 3 full blocks
	stream, _ = naiveAppend(B, stream, []byte("ok"))
	// Corrupt the middle fragment's data (block 1) -> checksum error there;
	// the Last fragment in block 2 then has no First -> sequence error.
	stream[B+HeaderSize] ^= 0xFF

	rd := mustReader(t, stream, B)
	var errs []error
	var recs [][]byte
	for {
		rec, _, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		recs = append(recs, rec)
	}
	if len(errs) != 2 || !errors.Is(errs[0], ErrChecksum) || !errors.Is(errs[1], ErrSequence) {
		t.Fatalf("want [ErrChecksum ErrSequence], got %v", errs)
	}
	if len(recs) != 1 || string(recs[0]) != "ok" {
		t.Fatalf("want only [ok] recovered, got %q", recs)
	}
	t.Logf("中片校验错 -> 丢弃整条在组记录; 末片因无首片再报序列错; 下一记录 %q 正常读回", recs[0])
}
